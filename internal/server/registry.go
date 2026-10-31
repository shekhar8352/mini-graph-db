package server

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

// openTx is one server-side transaction bound to a connection.
type openTx struct {
	id       int64
	db       string
	g        *graph.Graph
	readOnly bool
	started  time.Time
	last     atomic.Int64
	conn     uint64
	dbRef    *database
	owner    *Session
}

func (t *openTx) touch() {
	t.last.Store(time.Now().UnixNano())
}

func (t *openTx) idStr() string {
	return strconv.FormatInt(t.id, 10)
}

func (t *openTx) expired(now time.Time, idle time.Duration) bool {
	return now.Sub(time.Unix(0, t.last.Load())) > idle
}

// Session is the state of one gRPC connection.
// mu is held for the whole RPC so the connection's graph is not used concurrently.
type Session struct {
	mu   sync.Mutex
	conn uint64
	db   string
	tx   *openTx
	host *Host
}

type registry struct {
	mu       sync.Mutex
	seq      atomic.Int64
	sessions map[uint64]*Session
	txs      map[int64]*openTx
}

func newRegistry() *registry {
	return &registry{
		sessions: map[uint64]*Session{},
		txs:      map[int64]*openTx{},
	}
}

func (r *registry) session(id uint64, h *Host) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s
	}
	s := &Session{conn: id, db: "default", host: h}
	r.sessions[id] = s
	return s
}

func (r *registry) add(tx *openTx, max int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.txs) >= max {
		return gerr.New(gerr.ResourceExhausted, "too many open transactions")
	}
	r.txs[tx.id] = tx
	return nil
}

func (r *registry) remove(id int64) {
	r.mu.Lock()
	delete(r.txs, id)
	r.mu.Unlock()
}

func (r *registry) next() int64 {
	return r.seq.Add(1)
}

func (r *registry) hasDatabase(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tx := range r.txs {
		if tx.db == name {
			return true
		}
	}
	return false
}

func (r *registry) list() []*openTx {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*openTx, 0, len(r.txs))
	for _, tx := range r.txs {
		out = append(out, tx)
	}
	return out
}

func (r *registry) dropConn(id uint64) {
	r.mu.Lock()
	s := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.finishRollback()
	s.mu.Unlock()
}

func (r *registry) rollbackAll() {
	r.mu.Lock()
	sessions := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		s.finishRollback()
		s.mu.Unlock()
	}
}

func (r *registry) terminate(id int64) error {
	r.mu.Lock()
	tx := r.txs[id]
	r.mu.Unlock()
	if tx == nil || tx.owner == nil {
		return gerr.Newf(gerr.NotFound, "transaction %d", id)
	}
	tx.owner.mu.Lock()
	defer tx.owner.mu.Unlock()
	if tx.owner.tx != tx {
		return gerr.Newf(gerr.NotFound, "transaction %d", id)
	}
	return tx.owner.finishRollback()
}

func (r *registry) sweep(idle time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			r.expire(now, idle)
		}
	}
}

func (r *registry) expire(now time.Time, idle time.Duration) {
	r.mu.Lock()
	var old []*openTx
	for _, tx := range r.txs {
		if tx.expired(now, idle) {
			old = append(old, tx)
		}
	}
	r.mu.Unlock()
	for _, tx := range old {
		if tx.owner == nil {
			continue
		}
		tx.owner.mu.Lock()
		if tx.owner.tx == tx && tx.expired(time.Now(), idle) {
			_ = tx.owner.finishRollback()
		}
		tx.owner.mu.Unlock()
	}
}

// finishRollback drops the session transaction when one is open.
// The caller holds s.mu. A second call is a no-op.
func (s *Session) finishRollback() error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.g.Rollback()
	s.host.release(s.tx.dbRef)
	s.host.reg.remove(s.tx.id)
	s.tx = nil
	return err
}

func (s *Session) finishCommit() error {
	if s.tx == nil {
		return gerr.New(gerr.InvalidArgument, "no transaction")
	}
	if err := s.tx.g.Commit(); err != nil {
		return err
	}
	s.host.release(s.tx.dbRef)
	s.host.reg.remove(s.tx.id)
	s.tx = nil
	return nil
}

func (s *Session) adopt(db *database, g *graph.Graph) error {
	tx := &openTx{
		id:       s.host.reg.next(),
		db:       db.name,
		g:        g,
		readOnly: g.InReadOnlyTxn(),
		started:  time.Now(),
		conn:     s.conn,
		dbRef:    db,
		owner:    s,
	}
	tx.touch()
	if err := s.host.reg.add(tx, s.host.cfg.Limits.MaxTxn); err != nil {
		return err
	}
	s.tx = tx
	return nil
}

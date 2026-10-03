// Package txn is the transaction manager. It assigns snapshot and commit
// timestamps, keeps a private write set per transaction, and publishes
// versions with first-committer-wins. Graph records stay in graphstore;
// this package versions every key those records use.
package txn

import (
	"context"
	"sync"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

const (
	defaultMaxOpen  = 1024
	defaultMaxWrite = 100_000
)

// userKeyspaces are the keyspaces graphstore writes. KSVersion is owned by
// the manager and is not in this list.
var userKeyspaces = []storage.Keyspace{
	storage.KSNode,
	storage.KSEdge,
	storage.KSOut,
	storage.KSIn,
	storage.KSLabel,
	storage.KSEdgeType,
	storage.KSProp,
	storage.KSCatalog,
}

// Options configures limits and background vacuum. Zero values select the
// defaults noted on each field. A negative MaxOpenTxns or MaxWriteSet
// disables that cap. A zero IdleTimeout or GCInterval disables that timer.
type Options struct {
	// MaxOpenTxns is the cap on transactions that have not finished.
	// Zero selects 1024. Past the cap, Begin returns ResourceExhausted.
	MaxOpenTxns int
	// MaxWriteSet is the cap on keys in one write set.
	// Zero selects 100000. Past the cap, Put and Delete return ResourceExhausted.
	MaxWriteSet int
	// IdleTimeout aborts a transaction that has not been used for this long.
	// Zero disables the timeout.
	IdleTimeout time.Duration
	// GCInterval starts a background vacuum. Zero leaves vacuum to Vacuum.
	GCInterval time.Duration
}

// MVCCStats is the transaction-manager view of versions and snapshots.
// VersionsReclaimed is the versions_reclaimed_total counter.
// OldestSnapshotAge is the oldest_snapshot_age_seconds gauge.
type MVCCStats struct {
	VersionsReclaimed uint64
	OldestSnapshotAge time.Duration
	OpenTxns          int
	CommitTS          uint64
	LiveKeys          int
}

// AbortLogger is implemented by an engine that can append a TxnAbort record.
// The memory engine does not implement it. Recovery already treats a missing
// TxnCommit as an abort; the record makes the abort explicit in the log.
type AbortLogger interface {
	LogAbort(txnID uint64) error
}

// Manager assigns timestamps and publishes versioned writes on eng.
// It implements storage.Engine. The caller owns eng until Open returns;
// Close closes eng. KSVersion is reserved for chain records and the oracle.
type Manager struct {
	eng   storage.Engine
	opt   Options
	hooks storage.Hooks

	mu        sync.Mutex
	commitMu  sync.Mutex
	nextTxnID uint64
	commitTS  uint64
	liveKeys  int
	open      map[*Tx]struct{}
	spaces    map[storage.Keyspace]struct{}
	commits   uint64
	rollbacks uint64
	reclaimed uint64
	closed    bool

	gcStop chan struct{}
	gcDone chan struct{}
}

// Open prepares eng for versioned transactions. A database written before
// this manager existed is wrapped in place: each raw value becomes a head
// with createTS 1. An mvcc format newer than this build is refused.
func Open(eng storage.Engine, opt Options) (*Manager, error) {
	if eng == nil {
		return nil, gerr.New(gerr.InvalidArgument, "engine is nil")
	}
	opt = normalize(opt)
	if err := migrate(eng); err != nil {
		return nil, err
	}
	o, err := loadOracle(eng)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		eng:       eng,
		opt:       opt,
		nextTxnID: o.nextTxn,
		commitTS:  o.commitTS,
		open:      map[*Tx]struct{}{},
		spaces:    map[storage.Keyspace]struct{}{},
	}
	if m.nextTxnID == 0 {
		m.nextTxnID = 1
	}
	for _, ks := range userKeyspaces {
		m.spaces[ks] = struct{}{}
	}
	n, err := m.countLive()
	if err != nil {
		return nil, err
	}
	m.liveKeys = n
	m.startGC()
	return m, nil
}

func normalize(opt Options) Options {
	if opt.MaxOpenTxns == 0 {
		opt.MaxOpenTxns = defaultMaxOpen
	}
	if opt.MaxWriteSet == 0 {
		opt.MaxWriteSet = defaultMaxWrite
	}
	return opt
}

// Begin starts a transaction on the committed snapshot.
func (m *Manager) Begin(opts storage.TxOptions) (storage.Tx, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, storage.ErrClosed
	}
	if m.opt.MaxOpenTxns > 0 && len(m.open) >= m.opt.MaxOpenTxns {
		return nil, gerr.New(gerr.ResourceExhausted, "too many open transactions")
	}
	id := m.nextTxnID
	m.nextTxnID++
	tx := &Tx{
		m:        m,
		id:       id,
		snapshot: m.commitTS,
		ro:       opts.ReadOnly,
		writes:   map[storage.Keyspace]map[string]writeOp{},
		last:     time.Now(),
		started:  time.Now(),
	}
	m.open[tx] = struct{}{}
	return tx, nil
}

// Sync flushes the underlying engine.
func (m *Manager) Sync() error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return storage.ErrClosed
	}
	return m.eng.Sync()
}

// Stats reports committed logical keys. Version-chain records are not counted.
func (m *Manager) Stats() storage.Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return storage.Stats{
		Keys:      m.liveKeys,
		Commits:   m.commits,
		Rollbacks: m.rollbacks,
	}
}

// MVCCStats reports vacuum and snapshot gauges.
func (m *Manager) MVCCStats() MVCCStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MVCCStats{
		VersionsReclaimed: m.reclaimed,
		OldestSnapshotAge: m.oldestAgeLocked(time.Now()),
		OpenTxns:          len(m.open),
		CommitTS:          m.commitTS,
		LiveKeys:          m.liveKeys,
	}
}

func (m *Manager) oldestAgeLocked(now time.Time) time.Duration {
	var oldest time.Time
	for tx := range m.open {
		if oldest.IsZero() || tx.started.Before(oldest) {
			oldest = tx.started
		}
	}
	if oldest.IsZero() {
		return 0
	}
	return now.Sub(oldest)
}

// SetHooks installs crash-simulation hooks for the manager.
// Hooks run on the committing transaction and must not call back into the manager.
func (m *Manager) SetHooks(h storage.Hooks) {
	m.mu.Lock()
	m.hooks = h
	m.mu.Unlock()
}

// Crash aborts every open transaction. Committed versions stay.
func (m *Manager) Crash() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return storage.ErrClosed
	}
	txs := make([]*Tx, 0, len(m.open))
	for tx := range m.open {
		txs = append(txs, tx)
	}
	m.open = map[*Tx]struct{}{}
	m.rollbacks += uint64(len(txs))
	m.mu.Unlock()
	for _, tx := range txs {
		tx.mu.Lock()
		had := hasWrites(tx.writes)
		id := tx.id
		tx.done = true
		tx.writes = nil
		tx.mu.Unlock()
		if had {
			m.logAbort(id)
		}
	}
	return nil
}

// Close aborts open transactions, stops background vacuum, and closes eng.
// A second Close returns nil.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	txs := make([]*Tx, 0, len(m.open))
	for tx := range m.open {
		txs = append(txs, tx)
	}
	m.open = map[*Tx]struct{}{}
	stop := m.gcStop
	done := m.gcDone
	m.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	for _, tx := range txs {
		tx.mu.Lock()
		tx.done = true
		tx.writes = nil
		tx.mu.Unlock()
	}
	return m.eng.Close()
}

// Vacuum removes versions no open snapshot can read.
// It returns how many version records were removed.
func (m *Manager) Vacuum(ctx context.Context) (uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, storage.ErrClosed
	}
	oldest := m.commitTS
	for tx := range m.open {
		if tx.snapshot < oldest {
			oldest = tx.snapshot
		}
	}
	spaces := make([]storage.Keyspace, 0, len(m.spaces))
	for ks := range m.spaces {
		spaces = append(spaces, ks)
	}
	m.mu.Unlock()

	type dead struct {
		ks  storage.Keyspace
		key []byte
	}
	var heads []dead
	var chains [][]byte
	stx, err := m.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	for _, ks := range spaces {
		if err := ctx.Err(); err != nil {
			_ = stx.Rollback()
			return 0, err
		}
		err := eachHead(stx, ks, func(key []byte, h head) error {
			if h.delete != 0 && h.delete <= oldest {
				heads = append(heads, dead{ks: ks, key: clone(key)})
			}
			return nil
		})
		if err != nil {
			_ = stx.Rollback()
			return 0, err
		}
	}
	err = eachChain(stx, func(key []byte, h head) error {
		if h.delete != 0 && h.delete <= oldest {
			chains = append(chains, clone(key))
		}
		return nil
	})
	if err != nil {
		_ = stx.Rollback()
		return 0, err
	}
	if err := stx.Rollback(); err != nil {
		return 0, err
	}
	if len(heads) == 0 && len(chains) == 0 {
		return 0, nil
	}
	wtx, err := m.eng.Begin(storage.TxOptions{})
	if err != nil {
		return 0, err
	}
	for _, d := range heads {
		if err := wtx.Delete(d.ks, d.key); err != nil {
			_ = wtx.Rollback()
			return 0, err
		}
	}
	for _, k := range chains {
		if err := wtx.Delete(storage.KSVersion, k); err != nil {
			_ = wtx.Rollback()
			return 0, err
		}
	}
	if err := wtx.Commit(); err != nil {
		_ = wtx.Rollback()
		return 0, err
	}
	n := uint64(len(heads) + len(chains))
	m.mu.Lock()
	m.reclaimed += n
	m.mu.Unlock()
	return n, nil
}

func (m *Manager) startGC() {
	if m.opt.GCInterval <= 0 {
		return
	}
	m.gcStop = make(chan struct{})
	m.gcDone = make(chan struct{})
	go func() {
		defer close(m.gcDone)
		tick := time.NewTicker(m.opt.GCInterval)
		defer tick.Stop()
		for {
			select {
			case <-m.gcStop:
				return
			case <-tick.C:
				_, _ = m.Vacuum(context.Background())
			}
		}
	}()
}

func (m *Manager) drop(tx *Tx, rollback bool) {
	m.mu.Lock()
	delete(m.open, tx)
	if rollback {
		m.rollbacks++
	}
	m.mu.Unlock()
}

func (m *Manager) logAbort(txnID uint64) {
	lg, ok := m.eng.(AbortLogger)
	if !ok {
		return
	}
	_ = lg.LogAbort(txnID)
}

func (m *Manager) noteSpace(ks storage.Keyspace) {
	switch ks {
	case storage.KSNode, storage.KSEdge, storage.KSOut, storage.KSIn,
		storage.KSLabel, storage.KSEdgeType, storage.KSProp, storage.KSCatalog:
		return
	}
	m.mu.Lock()
	if _, ok := m.spaces[ks]; !ok {
		m.spaces[ks] = struct{}{}
	}
	m.mu.Unlock()
}

// HistoricVersions counts chain records. Tests and vacuum use it.
func (m *Manager) HistoricVersions() (int, error) {
	stx, err := m.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = stx.Rollback() }()
	n := 0
	err = eachChain(stx, func([]byte, head) error {
		n++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

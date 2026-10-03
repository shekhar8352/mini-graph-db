package disk

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

const (
	dbFileName = "db"
	walDirName = "wal"

	defaultPoolFrames      = 128
	defaultCheckpointEvery = 30 * time.Second
	defaultCheckpointBytes = 64 << 20
)

// errStopAfterSync is returned when a test asks Commit to sync the WAL and
// then return before publishing pages.
var errStopAfterSync = errors.New("disk: stop after wal sync")

// EngineOptions configures OpenEngine. Zero values select defaults.
// A negative checkpoint interval and a negative checkpoint byte limit disable
// the background checkpointer. Close still checkpoints a read-write engine.
type EngineOptions struct {
	// PageSize is fixed at creation. Zero selects the default 8 KiB page.
	PageSize int
	// PoolFrames is the buffer pool size. Zero selects 128.
	PoolFrames int
	// ReadOnly rejects read-write transactions. Open still replays the WAL,
	// which can write repaired pages.
	ReadOnly bool
	// WAL configures the write-ahead log. Zero values select its defaults.
	WAL wal.Options
	// CheckpointInterval is how often the background checkpointer runs.
	// Zero selects 30s. Negative disables the timer.
	CheckpointInterval time.Duration
	// CheckpointBytes starts a checkpoint when this many WAL bytes have been
	// synced since the last one. Zero selects 64 MiB. Negative disables it.
	CheckpointBytes int64
}

// Engine is the durable storage.Engine. A commit appends page images to the
// WAL, syncs them, and only then writes those pages to the heap. Open replays
// committed images that the last checkpoint does not cover.
//
// A cursor holds its tree's read lock until Close. Do not Get, Put, Delete,
// or Commit on that keyspace from the same goroutine while the cursor is open.
type Engine struct {
	commitMu sync.Mutex
	mu       sync.Mutex

	file     *PageFile
	pool     *Pool
	wal      *wal.Log
	trees    map[storage.Keyspace]*Tree
	hooks    storage.Hooks
	open     map[*diskTx]struct{}
	undo     map[undoKey][]undoRec
	gen      uint64
	nextTxn  uint64
	keys     int
	commits  uint64
	rollback uint64
	closed   bool
	readOnly bool
	broken   error

	ckptInterval time.Duration
	ckptBytes    int64
	stop         chan struct{}
	kick         chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once

	// stopAfterSync is a test hook. Commit syncs the WAL and returns
	// errStopAfterSync without publishing pages.
	stopAfterSync bool
}

type undoKey struct {
	ks  storage.Keyspace
	key string
}

type undoRec struct {
	gen     uint64
	val     []byte
	existed bool
}

type op struct {
	ks  storage.Keyspace
	key []byte
	val []byte
	del bool
}

type writeOp struct {
	val []byte
	del bool
}

type diskTx struct {
	eng    *Engine
	gen    uint64
	writes map[storage.Keyspace]map[string]writeOp
	ro     bool
	done   bool
	flag   atomic.Bool
}

// OpenEngine creates or opens a database directory. The heap file is db and the
// log segments live in wal/. A format version newer than this build is refused.
func OpenEngine(dir string, opt EngineOptions) (*Engine, error) {
	if dir == "" {
		return nil, gerr.New(gerr.InvalidArgument, "data directory is empty")
	}
	opt = normalizeEngineOptions(opt)
	pagePath := filepath.Join(dir, dbFileName)
	if opt.ReadOnly {
		if _, err := os.Stat(pagePath); err != nil {
			if os.IsNotExist(err) {
				return nil, gerr.Wrap(gerr.NotFound, pagePath, err)
			}
			return nil, gerr.Wrap(gerr.Unavailable, "stat page file", err)
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "create data directory", err)
	}

	var pf *PageFile
	var err error
	if _, statErr := os.Stat(pagePath); os.IsNotExist(statErr) {
		if opt.ReadOnly {
			return nil, gerr.Wrap(gerr.NotFound, pagePath, statErr)
		}
		pf, err = Create(pagePath, Options{PageSize: opt.PageSize})
	} else if statErr != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "stat page file", statErr)
	} else {
		pf, err = Open(pagePath, Options{PageSize: opt.PageSize, SkipFreelistCheck: true})
	}
	if err != nil {
		return nil, err
	}

	lg, err := wal.Open(filepath.Join(dir, walDirName), opt.WAL)
	if err != nil {
		_ = pf.Close()
		return nil, err
	}
	e := &Engine{
		file:         pf,
		wal:          lg,
		trees:        map[storage.Keyspace]*Tree{},
		open:         map[*diskTx]struct{}{},
		undo:         map[undoKey][]undoRec{},
		readOnly:     opt.ReadOnly,
		ckptInterval: opt.CheckpointInterval,
		ckptBytes:    opt.CheckpointBytes,
	}
	if err := e.recover(); err != nil {
		_ = lg.Close()
		_ = pf.Close()
		return nil, err
	}
	if err := pf.Sync(); err != nil {
		_ = lg.Close()
		_ = pf.Close()
		return nil, err
	}
	if err := pf.CheckFreelist(); err != nil {
		_ = lg.Close()
		_ = pf.Close()
		return nil, err
	}
	pool, err := NewPool(pf, opt.PoolFrames)
	if err != nil {
		_ = lg.Close()
		_ = pf.Close()
		return nil, err
	}
	e.pool = pool
	for _, ks := range rootOrder {
		tree, err := OpenTree(pf, pool, ks)
		if err != nil {
			_ = pool.Close()
			_ = lg.Close()
			_ = pf.Close()
			return nil, err
		}
		e.trees[ks] = tree
	}
	n, err := e.countKeys()
	if err != nil {
		_ = pool.Close()
		_ = lg.Close()
		_ = pf.Close()
		return nil, err
	}
	e.keys = n
	e.startCheckpointer()
	return e, nil
}

func normalizeEngineOptions(opt EngineOptions) EngineOptions {
	if opt.PoolFrames == 0 {
		opt.PoolFrames = defaultPoolFrames
	}
	if opt.CheckpointInterval == 0 {
		opt.CheckpointInterval = defaultCheckpointEvery
	}
	if opt.CheckpointBytes == 0 {
		opt.CheckpointBytes = defaultCheckpointBytes
	}
	return opt
}

var _ storage.Engine = (*Engine)(nil)

// Begin starts a transaction on the committed snapshot.
func (e *Engine) Begin(opts storage.TxOptions) (storage.Tx, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, storage.ErrClosed
	}
	if e.broken != nil {
		return nil, e.broken
	}
	if !opts.ReadOnly && e.readOnly {
		return nil, storage.ErrReadOnly
	}
	tx := &diskTx{
		eng:    e,
		gen:    e.gen,
		writes: map[storage.Keyspace]map[string]writeOp{},
		ro:     opts.ReadOnly,
	}
	e.open[tx] = struct{}{}
	return tx, nil
}

// Sync flushes the WAL and any dirty pages.
func (e *Engine) Sync() error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	if err := e.wal.Sync(); err != nil {
		return err
	}
	if err := e.pool.FlushAll(); err != nil {
		return err
	}
	return nil
}

// LogAbort appends a TxnAbort record and syncs the log.
// Recovery already drops a transaction that has no TxnCommit. The record
// names that abort. txn.Manager calls this when a write set is rolled back.
func (e *Engine) LogAbort(txnID uint64) error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	if e.readOnly {
		return storage.ErrReadOnly
	}
	if e.broken != nil {
		return e.broken
	}
	if _, err := e.wal.Append(wal.TypeTxnAbort, txnID, nil); err != nil {
		return err
	}
	return e.wal.Sync()
}

// Stats reports committed keys and commit counters.
func (e *Engine) Stats() storage.Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return storage.Stats{Keys: e.keys, Commits: e.commits, Rollbacks: e.rollback}
}

// SetHooks replaces crash-simulation hooks.
func (e *Engine) SetHooks(h storage.Hooks) {
	e.mu.Lock()
	e.hooks = h
	e.mu.Unlock()
}

// Crash aborts every open transaction. Committed data is kept.
func (e *Engine) Crash() error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	for tx := range e.open {
		tx.finish(e)
		e.rollback++
	}
	e.gcUndo()
	return nil
}

// Close checkpoints, then closes the files. A second Close returns nil.
// A broken engine drops dirty pages instead of checkpointing them.
func (e *Engine) Close() error {
	e.stopLoop()
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	broken := e.broken
	ro := e.readOnly
	e.mu.Unlock()
	if broken != nil {
		return e.shutdown(true)
	}
	if !ro {
		if err := e.checkpointLocked(); err != nil {
			_ = e.shutdown(true)
			return err
		}
	}
	return e.shutdown(false)
}

// Checkpoint flushes dirty pages, records the durable LSN in the header, and
// deletes sealed WAL segments that end at or before that LSN.
func (e *Engine) Checkpoint() error {
	if e.readOnly {
		return storage.ErrReadOnly
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	return e.checkpointLocked()
}

func (e *Engine) checkpointLocked() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	if e.broken != nil {
		return e.broken
	}
	if err := e.pool.FlushAll(); err != nil {
		return err
	}
	if _, err := e.wal.Append(wal.TypeCheckpoint, 0, wal.EncodeCheckpoint(time.Now().UnixNano())); err != nil {
		return err
	}
	if err := e.wal.Sync(); err != nil {
		return err
	}
	end := e.wal.Durable()
	if err := e.file.SetCheckpointLSN(uint64(end)); err != nil {
		return err
	}
	if err := e.file.Sync(); err != nil {
		return err
	}
	return e.wal.Truncate(end)
}

// abandon drops dirty pages and closes without checkpointing.
// Tests use it as a process crash. Synced WAL records stay on disk.
func (e *Engine) abandon() {
	e.stopLoop()
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	_ = e.shutdown(true)
}

func (e *Engine) shutdown(drop bool) error {
	e.mu.Lock()
	e.closed = true
	for tx := range e.open {
		tx.finish(e)
	}
	e.mu.Unlock()
	var err error
	if drop {
		if e.pool != nil {
			e.pool.Abandon()
		}
		if e.file != nil {
			e.file.Discard()
		}
		if e.wal != nil {
			e.wal.Abandon()
		}
	} else if e.pool != nil {
		if cerr := e.pool.Close(); cerr != nil {
			err = cerr
		}
	}
	if !drop && e.wal != nil {
		if cerr := e.wal.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if e.file != nil {
		if cerr := e.file.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func (e *Engine) startCheckpointer() {
	if e.readOnly || (e.ckptInterval <= 0 && e.ckptBytes <= 0) {
		return
	}
	e.stop = make(chan struct{})
	e.kick = make(chan struct{}, 1)
	e.stopped = make(chan struct{})
	go e.checkpointLoop()
}

func (e *Engine) checkpointLoop() {
	defer close(e.stopped)
	var ticks <-chan time.Time
	if e.ckptInterval > 0 {
		ticker := time.NewTicker(e.ckptInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		select {
		case <-e.stop:
			return
		case <-ticks:
			_ = e.Checkpoint()
		case <-e.kick:
			_ = e.Checkpoint()
		}
	}
}

func (e *Engine) stopLoop() {
	if e.stopped == nil {
		return
	}
	e.stopOnce.Do(func() { close(e.stop) })
	<-e.stopped
}

func (e *Engine) kickCheckpoint() {
	if e.kick == nil || e.ckptBytes <= 0 {
		return
	}
	pending := int64(e.wal.Durable()) - int64(e.file.CheckpointLSN())
	if pending < e.ckptBytes {
		return
	}
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) countKeys() (int, error) {
	n := 0
	for _, ks := range rootOrder {
		cur, err := e.trees[ks].Cursor()
		if err != nil {
			return 0, err
		}
		if cur.Seek(nil) {
			for {
				n++
				if !cur.Next() {
					break
				}
			}
		}
		if err := cur.Err(); err != nil {
			_ = cur.Close()
			return 0, err
		}
		if err := cur.Close(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (t *diskTx) finish(e *Engine) {
	t.done = true
	t.flag.Store(true)
	t.writes = nil
	delete(e.open, t)
}

func (e *Engine) noteUndo(gen uint64, ks storage.Keyspace, key, val []byte, existed bool) {
	k := undoKey{ks: ks, key: string(key)}
	e.undo[k] = append(e.undo[k], undoRec{gen: gen, val: cloneBytes(val), existed: existed})
}

func (e *Engine) gcUndo() {
	oldest := e.gen
	for tx := range e.open {
		if tx.gen < oldest {
			oldest = tx.gen
		}
	}
	for k, recs := range e.undo {
		kept := recs[:0]
		for _, rec := range recs {
			if rec.gen > oldest {
				kept = append(kept, rec)
			}
		}
		if len(kept) == 0 {
			delete(e.undo, k)
			continue
		}
		e.undo[k] = append([]undoRec(nil), kept...)
	}
}

func (e *Engine) oldestUndo(gen uint64, ks storage.Keyspace, key []byte) (undoRec, bool) {
	recs := e.undo[undoKey{ks: ks, key: string(key)}]
	var best undoRec
	found := false
	for _, rec := range recs {
		if rec.gen <= gen {
			continue
		}
		if !found || rec.gen < best.gen {
			best = rec
			found = true
		}
	}
	return best, found
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func collectOps(writes map[storage.Keyspace]map[string]writeOp) []op {
	var ops []op
	for ks, m := range writes {
		for k, opv := range m {
			ops = append(ops, op{ks: ks, key: []byte(k), val: cloneBytes(opv.val), del: opv.del})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].ks != ops[j].ks {
			return ops[i].ks < ops[j].ks
		}
		return bytes.Compare(ops[i].key, ops[j].key) < 0
	})
	return ops
}

func hasOps(writes map[storage.Keyspace]map[string]writeOp) bool {
	for _, m := range writes {
		if len(m) > 0 {
			return true
		}
	}
	return false
}

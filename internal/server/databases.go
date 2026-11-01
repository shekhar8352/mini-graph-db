package server

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/config"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
	"github.com/shekhar8352/mini-graph-db/internal/txn"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

// kindDatabase marks a database-directory record in the system catalog.
// 0x20–0x27 are schema objects. 0x40 is the server's database list.
const kindDatabase byte = 0x40

var dbNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// dbInfo is one catalog row.
type dbInfo struct {
	name    string
	created int64
}

// database is one open or catalogued database.
type database struct {
	name    string
	dir     string
	created int64
	eng     *disk.Engine
	mgr     *txn.Manager
	refs    int
}

// Host owns the data directory, the system catalog, and server-side transactions.
type Host struct {
	cfg      config.Config
	mu       sync.Mutex
	dbs      map[string]*database
	sys      *database
	reg      *registry
	closed   bool
	stop     chan struct{}
	stopOnce sync.Once

	conns    atomic.Int64
	nextConn atomic.Uint64
	metrics  Metrics
}

// OpenHost creates the data directory, opens the system database, and ensures default exists.
func OpenHost(cfg config.Config) (*Host, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Storage.DataDir, 0o755); err != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "create data directory", err)
	}
	h := &Host{
		cfg:  cfg,
		dbs:  map[string]*database{},
		reg:  newRegistry(),
		stop: make(chan struct{}),
	}
	sys, err := h.openEngine("system")
	if err != nil {
		return nil, err
	}
	h.sys = sys
	h.dbs["system"] = sys
	if err := h.bootstrap(); err != nil {
		_ = h.Close()
		return nil, err
	}
	go h.trashLoop()
	go h.reg.sweep(h.cfg.Limits.TxnIdle, h.stop)
	return h, nil
}

func (h *Host) bootstrap() error {
	rows, err := h.readRecords()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.name == "system" {
			h.sys.created = row.created
			continue
		}
		h.dbs[row.name] = &database{
			name:    row.name,
			dir:     h.dbDir(row.name),
			created: row.created,
		}
	}
	if h.sys.created == 0 {
		h.sys.created = time.Now().UnixNano()
		if err := h.putRecord("system", h.sys.created); err != nil {
			return err
		}
	}
	if err := h.forgetMissing(); err != nil {
		return err
	}
	if _, ok := h.dbs["default"]; !ok {
		if err := h.createLocked("default", true); err != nil {
			return err
		}
	}
	return h.sweepOrphans()
}

func (h *Host) forgetMissing() error {
	var gone []string
	for name, db := range h.dbs {
		if name == "system" {
			continue
		}
		if _, err := os.Stat(filepath.Join(db.dir, "db")); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return gerr.Wrap(gerr.Unavailable, "stat database", err)
		}
		gone = append(gone, name)
	}
	for _, name := range gone {
		slog.Warn("database directory is missing; removing the catalog entry", "database", name)
		if err := h.deleteRecord(name); err != nil {
			return err
		}
		delete(h.dbs, name)
	}
	return nil
}

// Close stops background work, rolls transactions back, and checkpoints every database.
func (h *Host) Close() error {
	h.stopOnce.Do(func() { close(h.stop) })
	h.reg.rollbackAll()
	h.mu.Lock()
	h.closed = true
	dbs := h.dbs
	h.dbs = map[string]*database{}
	h.mu.Unlock()
	var err error
	for _, db := range dbs {
		if db.mgr == nil {
			continue
		}
		if cerr := db.mgr.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

// CreateDatabase creates a user database. system and default are reserved.
func (h *Host) CreateDatabase(name string) error {
	if !validDBName(name) {
		return gerr.Newf(gerr.Semantic, "database name %s", name)
	}
	if reservedDB(name) {
		return gerr.Newf(gerr.Semantic, "database name %s is reserved", name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.createLocked(name, false)
}

// DropDatabase moves a user database into data/.trash after confirm is true.
func (h *Host) DropDatabase(name string, confirm bool) error {
	if !confirm {
		return gerr.New(gerr.Semantic, "DROP DATABASE requires CONFIRM")
	}
	if !validDBName(name) {
		return gerr.Newf(gerr.Semantic, "database name %s", name)
	}
	if reservedDB(name) {
		return gerr.Newf(gerr.Semantic, "database name %s is reserved", name)
	}
	if h.reg.hasDatabase(name) {
		return gerr.New(gerr.InvalidArgument, "database has open transactions")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return gerr.New(gerr.Unavailable, "server is closed")
	}
	db := h.dbs[name]
	if db == nil {
		return gerr.Newf(gerr.NotFound, "database %s", name)
	}
	if db.refs > 0 {
		return gerr.New(gerr.InvalidArgument, "database is in use")
	}
	if db.mgr != nil {
		if err := db.mgr.Close(); err != nil {
			return err
		}
		db.mgr = nil
		db.eng = nil
	}
	if err := h.moveToTrash(db.dir, name); err != nil {
		return err
	}
	if err := h.deleteRecord(name); err != nil {
		return err
	}
	delete(h.dbs, name)
	return nil
}

// listDatabases returns every catalogued database, sorted by name.
func (h *Host) listDatabases() []dbInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]dbInfo, 0, len(h.dbs))
	for name, db := range h.dbs {
		out = append(out, dbInfo{name: name, created: db.created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (h *Host) createLocked(name string, allowReserved bool) error {
	if h.closed {
		return gerr.New(gerr.Unavailable, "server is closed")
	}
	if !allowReserved && reservedDB(name) {
		return gerr.Newf(gerr.Semantic, "database name %s is reserved", name)
	}
	if _, ok := h.dbs[name]; ok {
		return gerr.Newf(gerr.AlreadyExists, "database %s", name)
	}
	dir := h.dbDir(name)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return gerr.Newf(gerr.AlreadyExists, "database %s", name)
	}
	opened, err := h.openEngine(name)
	if err != nil {
		return err
	}
	created := time.Now().UnixNano()
	if err := h.putRecord(name, created); err != nil {
		_ = opened.mgr.Close()
		_ = os.RemoveAll(dir)
		return err
	}
	opened.created = created
	h.dbs[name] = opened
	return nil
}

func (h *Host) acquire(name string) (*database, error) {
	if !validDBName(name) {
		return nil, gerr.Newf(gerr.Semantic, "database name %s", name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, gerr.New(gerr.Unavailable, "server is closed")
	}
	db := h.dbs[name]
	if db == nil {
		return nil, gerr.Newf(gerr.NotFound, "database %s", name)
	}
	if db.mgr == nil {
		opened, err := h.openEngine(name)
		if err != nil {
			return nil, err
		}
		opened.created = db.created
		h.dbs[name] = opened
		db = opened
	}
	db.refs++
	return db, nil
}

func (h *Host) release(db *database) {
	if db == nil {
		return
	}
	h.mu.Lock()
	db.refs--
	if db.refs < 0 {
		db.refs = 0
	}
	h.mu.Unlock()
}

func (h *Host) hasDB(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.dbs[name]
	return ok
}

// Checkpoint flushes one database, or every open database when name is empty.
func (h *Host) Checkpoint(name string) error {
	if name == "" {
		h.mu.Lock()
		var names []string
		for n, db := range h.dbs {
			if db.eng != nil {
				names = append(names, n)
			}
		}
		h.mu.Unlock()
		sort.Strings(names)
		for _, n := range names {
			if err := h.Checkpoint(n); err != nil {
				return err
			}
		}
		return nil
	}
	db, err := h.acquire(name)
	if err != nil {
		return err
	}
	defer h.release(db)
	return db.eng.Checkpoint()
}

// Vacuum removes versions no open snapshot on database can read.
func (h *Host) Vacuum(ctx context.Context, name string) (uint64, error) {
	if name == "" {
		name = "default"
	}
	db, err := h.acquire(name)
	if err != nil {
		return 0, err
	}
	defer h.release(db)
	return db.mgr.Vacuum(ctx)
}

// stats reads cardinality for one database. An empty name selects default.
func (h *Host) stats(name string) (dbStats, error) {
	if name == "" {
		name = "default"
	}
	db, err := h.acquire(name)
	if err != nil {
		return dbStats{}, err
	}
	defer h.release(db)
	g := graph.NewWith(db.mgr)
	st := g.Stats()
	ss := db.mgr.Stats()
	mv := db.mgr.MVCCStats()
	sort.Strings(st.Labels)
	return dbStats{
		name:      name,
		nodes:     int64(st.Nodes),
		edges:     int64(st.Edges),
		labels:    st.Labels,
		keys:      int64(ss.Keys),
		commits:   ss.Commits,
		rollbacks: ss.Rollbacks,
		reclaimed: mv.VersionsReclaimed,
	}, nil
}

type dbStats struct {
	name      string
	nodes     int64
	edges     int64
	labels    []string
	keys      int64
	commits   uint64
	rollbacks uint64
	reclaimed uint64
}

func (h *Host) openEngine(name string) (*database, error) {
	dir := h.dbDir(name)
	frames := int(h.cfg.Storage.PageCacheBytes / disk.DefaultPageSize)
	if frames < 16 {
		frames = 16
	}
	eng, err := disk.OpenEngine(dir, disk.EngineOptions{
		PoolFrames:         frames,
		CheckpointInterval: h.cfg.Storage.CheckpointInterval,
		WAL: wal.Options{
			SegmentSize: h.cfg.Storage.WALSegmentBytes,
			NoFsync:     !h.cfg.Storage.Fsync,
		},
	})
	if err != nil {
		return nil, err
	}
	mgr, err := txn.Open(eng, txn.Options{MaxOpenTxns: h.cfg.Limits.MaxTxn})
	if err != nil {
		_ = eng.Close()
		return nil, err
	}
	return &database{name: name, dir: dir, eng: eng, mgr: mgr}, nil
}

func (h *Host) dbDir(name string) string {
	return filepath.Join(h.cfg.Storage.DataDir, name)
}

func (h *Host) readRecords() ([]dbInfo, error) {
	tx, err := h.sys.mgr.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := tx.Cursor(storage.KSCatalog)
	if err != nil {
		return nil, err
	}
	var out []dbInfo
	if cur.Seek([]byte{kindDatabase}) {
		for {
			k := append([]byte(nil), cur.Key()...)
			if len(k) == 0 || k[0] != kindDatabase {
				break
			}
			raw := append([]byte(nil), cur.Value()...)
			created, derr := decodeDB(raw)
			if derr != nil {
				_ = cur.Close()
				return nil, derr
			}
			out = append(out, dbInfo{name: string(k[1:]), created: created})
			if !cur.Next() {
				break
			}
		}
	}
	if err := cur.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Host) putRecord(name string, created int64) error {
	tx, err := h.sys.mgr.Begin(storage.TxOptions{})
	if err != nil {
		return err
	}
	if err := tx.Put(storage.KSCatalog, dbKey(name), encodeDB(created)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (h *Host) deleteRecord(name string) error {
	tx, err := h.sys.mgr.Begin(storage.TxOptions{})
	if err != nil {
		return err
	}
	if err := tx.Delete(storage.KSCatalog, dbKey(name)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (h *Host) moveToTrash(dir, name string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	trash := filepath.Join(h.cfg.Storage.DataDir, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return gerr.Wrap(gerr.Unavailable, "create trash", err)
	}
	dest := filepath.Join(trash, name+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Rename(dir, dest); err != nil {
		return gerr.Wrap(gerr.Unavailable, "move database to trash", err)
	}
	return nil
}

func (h *Host) sweepOrphans() error {
	entries, err := os.ReadDir(h.cfg.Storage.DataDir)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "read data directory", err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		name := ent.Name()
		if name == ".trash" || !validDBName(name) {
			continue
		}
		if _, ok := h.dbs[name]; ok {
			continue
		}
		slog.Warn("moving uncatalogued database directory to trash", "database", name)
		if err := h.moveToTrash(h.dbDir(name), name); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) trashLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	h.sweepTrash()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.sweepTrash()
		}
	}
}

func (h *Host) sweepTrash() {
	trash := filepath.Join(h.cfg.Storage.DataDir, ".trash")
	entries, err := os.ReadDir(trash)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-h.cfg.Storage.TrashRetention)
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if h.cfg.Storage.TrashRetention == 0 || info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(trash, ent.Name()))
		}
	}
}

func (h *Host) graph(db *database) *graph.Graph {
	return graph.NewWith(db.mgr)
}

func validDBName(name string) bool {
	return dbNameRe.MatchString(name)
}

func reservedDB(name string) bool {
	return name == "system" || name == "default"
}

func dbKey(name string) []byte {
	return append([]byte{kindDatabase}, name...)
}

func encodeDB(created int64) []byte {
	buf := make([]byte, 9)
	buf[0] = 1
	binary.BigEndian.PutUint64(buf[1:], uint64(created))
	return buf
}

func decodeDB(raw []byte) (int64, error) {
	if len(raw) != 9 || raw[0] != 1 {
		return 0, gerr.New(gerr.Corruption, "database catalog record")
	}
	return int64(binary.BigEndian.Uint64(raw[1:])), nil
}

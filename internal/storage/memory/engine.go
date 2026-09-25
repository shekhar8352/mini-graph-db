// Package memory is the in-memory storage.Engine used by tests and the
// embedded shell until the disk engine lands.
package memory

import (
	"bytes"
	"sort"
	"sync"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

// Engine is a process-local engine. Each keyspace is a sorted key slice plus
// a value map. Commits publish a new snapshot; open readers keep the snapshot
// they started with. A Tx must be used from one goroutine.
type Engine struct {
	mu        sync.Mutex
	current   *snapshot
	hooks     storage.Hooks
	open      map[*memTx]struct{}
	closed    bool
	commits   uint64
	rollbacks uint64
}

type snapshot struct {
	spaces map[storage.Keyspace]*space
}

type space struct {
	keys [][]byte
	vals map[string][]byte
}

type writeOp struct {
	val []byte
	del bool
}

type memTx struct {
	eng    *Engine
	snap   *snapshot
	writes map[storage.Keyspace]map[string]writeOp
	ro     bool
	done   bool
}

type memCursor struct {
	tx     *memTx
	ks     storage.Keyspace
	keys   [][]byte
	vals   [][]byte
	idx    int
	closed bool
}

// Open returns an empty engine. Node and edge ids are allocated by graphstore,
// not here.
func Open() *Engine {
	return &Engine{
		current: &snapshot{spaces: map[storage.Keyspace]*space{}},
		open:    map[*memTx]struct{}{},
	}
}

var _ storage.Engine = (*Engine)(nil)

// Begin starts a transaction on the current committed snapshot.
func (e *Engine) Begin(opts storage.TxOptions) (storage.Tx, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, storage.ErrClosed
	}
	tx := &memTx{
		eng:    e,
		snap:   e.current,
		writes: map[storage.Keyspace]map[string]writeOp{},
		ro:     opts.ReadOnly,
	}
	e.open[tx] = struct{}{}
	return tx, nil
}

// Sync has no file to flush.
func (e *Engine) Sync() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	return nil
}

// Stats reports the committed snapshot.
func (e *Engine) Stats() storage.Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return storage.Stats{
		Keys:      e.current.keyCount(),
		Commits:   e.commits,
		Rollbacks: e.rollbacks,
	}
}

// SetHooks replaces crash-simulation hooks.
func (e *Engine) SetHooks(h storage.Hooks) {
	e.mu.Lock()
	e.hooks = h
	e.mu.Unlock()
}

// Crash aborts every open transaction and leaves committed data in place.
func (e *Engine) Crash() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return storage.ErrClosed
	}
	for tx := range e.open {
		tx.done = true
		tx.writes = nil
		e.rollbacks++
	}
	e.open = map[*memTx]struct{}{}
	return nil
}

// Close aborts open transactions. A second Close returns nil.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	for tx := range e.open {
		tx.done = true
		tx.writes = nil
	}
	e.open = map[*memTx]struct{}{}
	e.closed = true
	return nil
}

// Get returns a copy of the value visible to this transaction.
func (t *memTx) Get(ks storage.Keyspace, key []byte) ([]byte, error) {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return nil, storage.ErrDone
	}
	if op, ok := t.writes[ks][string(key)]; ok {
		if op.del {
			return nil, storage.ErrNotFound
		}
		return copyBytes(op.val), nil
	}
	sp := t.snap.spaces[ks]
	if sp == nil {
		return nil, storage.ErrNotFound
	}
	v, ok := sp.vals[string(key)]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return copyBytes(v), nil
}

// Put buffers a write until Commit.
func (t *memTx) Put(ks storage.Keyspace, key, val []byte) error {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	if t.ro {
		return storage.ErrReadOnly
	}
	if val == nil {
		val = []byte{}
	}
	if t.writes[ks] == nil {
		t.writes[ks] = map[string]writeOp{}
	}
	t.writes[ks][string(key)] = writeOp{val: append([]byte(nil), val...)}
	return nil
}

// Delete buffers a tombstone until Commit.
func (t *memTx) Delete(ks storage.Keyspace, key []byte) error {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	if t.ro {
		return storage.ErrReadOnly
	}
	if t.writes[ks] == nil {
		t.writes[ks] = map[string]writeOp{}
	}
	t.writes[ks][string(key)] = writeOp{del: true}
	return nil
}

// Cursor returns an unpositioned cursor.
func (t *memTx) Cursor(ks storage.Keyspace) (storage.Cursor, error) {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return nil, storage.ErrDone
	}
	return &memCursor{tx: t, ks: ks, idx: -1}, nil
}

// Commit publishes this transaction's writes onto the latest snapshot.
// Last writer wins per key. Conflict detection is Phase 3.
func (t *memTx) Commit() error {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.eng.closed {
		return storage.ErrClosed
	}
	if t.done {
		return storage.ErrDone
	}
	if t.ro || len(t.writes) == 0 {
		t.finishLocked()
		t.eng.commits++
		return nil
	}
	if h := t.eng.hooks.BeforeCommit; h != nil {
		if err := h(); err != nil {
			return err
		}
	}
	next := t.eng.current.clone()
	for ks, ops := range t.writes {
		sp := next.ensure(ks)
		for k, op := range ops {
			if op.del {
				sp.del(k)
				continue
			}
			sp.put(k, op.val)
		}
	}
	t.eng.current = next
	t.finishLocked()
	t.eng.commits++
	if h := t.eng.hooks.AfterCommit; h != nil {
		if err := h(); err != nil {
			return err
		}
	}
	return nil
}

// Rollback drops the write set.
func (t *memTx) Rollback() error {
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	t.finishLocked()
	t.eng.rollbacks++
	return nil
}

func (t *memTx) finishLocked() {
	t.done = true
	t.writes = nil
	delete(t.eng.open, t)
}

func (t *memTx) materialize(ks storage.Keyspace) ([][]byte, [][]byte) {
	sp := t.snap.spaces[ks]
	overlay := t.writes[ks]
	type kv struct {
		k []byte
		v []byte
	}
	var items []kv
	seen := map[string]struct{}{}
	if sp != nil {
		for _, k := range sp.keys {
			s := string(k)
			seen[s] = struct{}{}
			if op, ok := overlay[s]; ok {
				if op.del {
					continue
				}
				items = append(items, kv{append([]byte(nil), k...), append([]byte(nil), op.val...)})
				continue
			}
			items = append(items, kv{append([]byte(nil), k...), append([]byte(nil), sp.vals[s]...)})
		}
	}
	var extra []kv
	for s, op := range overlay {
		if op.del {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		extra = append(extra, kv{[]byte(s), append([]byte(nil), op.val...)})
	}
	sort.Slice(extra, func(i, j int) bool {
		return bytes.Compare(extra[i].k, extra[j].k) < 0
	})
	merged := make([]kv, 0, len(items)+len(extra))
	i, j := 0, 0
	for i < len(items) && j < len(extra) {
		if bytes.Compare(items[i].k, extra[j].k) <= 0 {
			merged = append(merged, items[i])
			i++
			continue
		}
		merged = append(merged, extra[j])
		j++
	}
	merged = append(merged, items[i:]...)
	merged = append(merged, extra[j:]...)
	keys := make([][]byte, len(merged))
	vals := make([][]byte, len(merged))
	for n, it := range merged {
		keys[n] = it.k
		vals[n] = it.v
	}
	return keys, vals
}

func (s *snapshot) clone() *snapshot {
	n := &snapshot{spaces: make(map[storage.Keyspace]*space, len(s.spaces))}
	for ks, sp := range s.spaces {
		n.spaces[ks] = sp.clone()
	}
	return n
}

func (s *snapshot) ensure(ks storage.Keyspace) *space {
	sp := s.spaces[ks]
	if sp == nil {
		sp = &space{vals: map[string][]byte{}}
		s.spaces[ks] = sp
	}
	return sp
}

func (s *snapshot) keyCount() int {
	n := 0
	for _, sp := range s.spaces {
		if sp != nil {
			n += len(sp.keys)
		}
	}
	return n
}

func (s *space) clone() *space {
	if s == nil {
		return &space{vals: map[string][]byte{}}
	}
	n := &space{
		keys: make([][]byte, len(s.keys)),
		vals: make(map[string][]byte, len(s.vals)),
	}
	for i, k := range s.keys {
		n.keys[i] = append([]byte(nil), k...)
	}
	for k, v := range s.vals {
		n.vals[k] = append([]byte(nil), v...)
	}
	return n
}

func (s *space) put(key string, val []byte) {
	kb := []byte(key)
	i := sort.Search(len(s.keys), func(i int) bool {
		return bytes.Compare(s.keys[i], kb) >= 0
	})
	if s.vals == nil {
		s.vals = map[string][]byte{}
	}
	if i < len(s.keys) && bytes.Equal(s.keys[i], kb) {
		s.vals[key] = append([]byte(nil), val...)
		return
	}
	s.keys = append(s.keys, nil)
	copy(s.keys[i+1:], s.keys[i:])
	s.keys[i] = append([]byte(nil), kb...)
	s.vals[key] = append([]byte(nil), val...)
}

func (s *space) del(key string) {
	kb := []byte(key)
	i := sort.Search(len(s.keys), func(i int) bool {
		return bytes.Compare(s.keys[i], kb) >= 0
	})
	if i >= len(s.keys) || !bytes.Equal(s.keys[i], kb) {
		return
	}
	s.keys = append(s.keys[:i], s.keys[i+1:]...)
	delete(s.vals, key)
}

func (c *memCursor) Seek(target []byte) bool {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.usable() {
		c.idx = -1
		return false
	}
	c.keys, c.vals = c.tx.materialize(c.ks)
	i := 0
	if target != nil {
		i = sort.Search(len(c.keys), func(n int) bool {
			return bytes.Compare(c.keys[n], target) >= 0
		})
	}
	if i >= len(c.keys) {
		c.idx = -1
		return false
	}
	c.idx = i
	return true
}

func (c *memCursor) SeekReverse(target []byte) bool {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.usable() {
		c.idx = -1
		return false
	}
	c.keys, c.vals = c.tx.materialize(c.ks)
	if len(c.keys) == 0 {
		c.idx = -1
		return false
	}
	if target == nil {
		c.idx = len(c.keys) - 1
		return true
	}
	i := sort.Search(len(c.keys), func(n int) bool {
		return bytes.Compare(c.keys[n], target) > 0
	})
	if i == 0 {
		c.idx = -1
		return false
	}
	c.idx = i - 1
	return true
}

func (c *memCursor) Next() bool {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.usable() || c.idx < 0 {
		c.idx = -1
		return false
	}
	c.idx++
	if c.idx >= len(c.keys) {
		c.idx = -1
		return false
	}
	return true
}

func (c *memCursor) Prev() bool {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.usable() || c.idx < 0 {
		c.idx = -1
		return false
	}
	c.idx--
	if c.idx < 0 {
		c.idx = -1
		return false
	}
	return true
}

func (c *memCursor) Key() []byte {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.positioned() {
		return nil
	}
	return copyBytes(c.keys[c.idx])
}

func (c *memCursor) Value() []byte {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	if !c.positioned() {
		return nil
	}
	return copyBytes(c.vals[c.idx])
}

func (c *memCursor) Valid() bool {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	return c.positioned()
}

func (c *memCursor) Close() error {
	c.tx.eng.mu.Lock()
	defer c.tx.eng.mu.Unlock()
	c.closed = true
	c.idx = -1
	return nil
}

func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func (c *memCursor) usable() bool {
	return !c.closed && !c.tx.done
}

func (c *memCursor) positioned() bool {
	return c.usable() && c.idx >= 0 && c.idx < len(c.keys)
}

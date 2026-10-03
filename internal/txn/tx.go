package txn

import (
	"bytes"
	"sort"
	"sync"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

type writeOp struct {
	val      []byte
	del      bool
	unique   bool
	shadowed bool
}

// Tx is one snapshot-isolated transaction. It is not safe for concurrent use.
// Writes stay private until Commit. Commit publishes them at a new timestamp
// or returns Conflict when another commit changed the same key.
type Tx struct {
	m        *Manager
	mu       sync.Mutex
	id       uint64
	snapshot uint64
	commitTS uint64
	ro       bool
	done     bool
	writes   map[storage.Keyspace]map[string]writeOp
	last     time.Time
	started  time.Time
}

// ID is the transaction id assigned at Begin.
func (t *Tx) ID() uint64 { return t.id }

// Snapshot is the commit timestamp this transaction reads.
func (t *Tx) Snapshot() uint64 { return t.snapshot }

// CommitTS is the timestamp assigned by a successful Commit. It is 0 before that.
func (t *Tx) CommitTS() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commitTS
}

// Get returns the value visible to this snapshot, including this transaction's writes.
func (t *Tx) Get(ks storage.Keyspace, key []byte) ([]byte, error) {
	if err := checkKS(ks); err != nil {
		return nil, err
	}
	if err := t.touch(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return nil, storage.ErrDone
	}
	if op, ok := t.writes[ks][string(key)]; ok {
		del := op.del
		val := payloadCopy(op.val)
		t.mu.Unlock()
		if del {
			return nil, storage.ErrNotFound
		}
		return val, nil
	}
	snap := t.snapshot
	t.mu.Unlock()
	return t.m.readAt(ks, key, snap)
}

// Put buffers a write. A nil value stores an empty present value.
func (t *Tx) Put(ks storage.Keyspace, key, val []byte) error {
	return t.put(ks, key, val, false)
}

// InsertUnique buffers a write that must be the only live value of key at commit.
// A key that is already visible, or that a concurrent transaction commits,
// fails with ConstraintViolation.
func (t *Tx) InsertUnique(ks storage.Keyspace, key, val []byte) error {
	if err := checkKS(ks); err != nil {
		return err
	}
	if err := t.touch(); err != nil {
		return err
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	if t.ro {
		t.mu.Unlock()
		return storage.ErrReadOnly
	}
	var shadowed bool
	if op, ok := t.writes[ks][string(key)]; ok {
		if !op.del {
			t.mu.Unlock()
			return gerr.New(gerr.ConstraintViolation, "unique key already exists")
		}
		shadowed = true
	}
	snap := t.snapshot
	t.mu.Unlock()
	if !shadowed {
		if _, err := t.m.readAt(ks, key, snap); err == nil {
			return gerr.New(gerr.ConstraintViolation, "unique key already exists")
		} else if !isNotFound(err) {
			return err
		}
	}
	return t.put(ks, key, val, true)
}

// Delete buffers a tombstone. Deleting a missing key succeeds.
func (t *Tx) Delete(ks storage.Keyspace, key []byte) error {
	if err := checkKS(ks); err != nil {
		return err
	}
	if err := t.touch(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	if t.ro {
		return storage.ErrReadOnly
	}
	if err := t.reserveLocked(ks, key); err != nil {
		return err
	}
	t.writes[ks][string(key)] = writeOp{del: true}
	return nil
}

func (t *Tx) put(ks storage.Keyspace, key, val []byte, unique bool) error {
	if err := checkKS(ks); err != nil {
		return err
	}
	if err := t.touch(); err != nil {
		return err
	}
	if val == nil {
		val = []byte{}
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	if t.ro {
		t.mu.Unlock()
		return storage.ErrReadOnly
	}
	if err := t.reserveLocked(ks, key); err != nil {
		t.mu.Unlock()
		return err
	}
	prev := t.writes[ks][string(key)]
	op := writeOp{
		val:      clone(val),
		unique:   unique || prev.unique,
		shadowed: prev.shadowed || prev.del,
	}
	t.writes[ks][string(key)] = op
	t.mu.Unlock()
	t.m.noteSpace(ks)
	return nil
}

func (t *Tx) reserveLocked(ks storage.Keyspace, key []byte) error {
	if _, ok := t.writes[ks][string(key)]; ok {
		return nil
	}
	if t.m.opt.MaxWriteSet > 0 && writeCount(t.writes) >= t.m.opt.MaxWriteSet {
		return gerr.New(gerr.ResourceExhausted, "transaction write set is full")
	}
	if t.writes[ks] == nil {
		t.writes[ks] = map[string]writeOp{}
	}
	return nil
}

// Cursor returns an unpositioned cursor over the snapshot plus this write set.
func (t *Tx) Cursor(ks storage.Keyspace) (storage.Cursor, error) {
	if err := checkKS(ks); err != nil {
		return nil, err
	}
	if err := t.touch(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil, storage.ErrDone
	}
	return &cursor{tx: t, ks: ks, idx: -1}, nil
}

// Commit validates the write set, assigns a commit timestamp, and publishes
// versions in one underlying transaction. A conflict leaves this transaction open.
func (t *Tx) Commit() error {
	if err := t.touch(); err != nil {
		return err
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	writes := cloneWrites(t.writes)
	ro := t.ro
	t.mu.Unlock()

	if ro || !hasWrites(writes) {
		t.finish(false)
		return nil
	}

	t.m.commitMu.Lock()
	defer t.m.commitMu.Unlock()

	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	t.mu.Unlock()

	t.m.mu.Lock()
	hook := t.m.hooks.BeforeCommit
	t.m.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}

	stx, err := t.m.eng.Begin(storage.TxOptions{})
	if err != nil {
		return err
	}
	if err := validate(stx, t.snapshot, writes); err != nil {
		_ = stx.Rollback()
		return err
	}
	t.m.mu.Lock()
	cts := t.m.commitTS + 1
	t.m.mu.Unlock()
	delta, err := publish(stx, cts, writes)
	if err != nil {
		_ = stx.Rollback()
		return err
	}
	t.m.mu.Lock()
	next := t.m.nextTxnID
	t.m.mu.Unlock()
	if err := stx.Put(storage.KSVersion, metaKey, encodeOracle(oracle{nextTxn: next, commitTS: cts})); err != nil {
		_ = stx.Rollback()
		return err
	}

	t.mu.Lock()
	crashed := t.done
	t.mu.Unlock()
	if crashed {
		_ = stx.Rollback()
		return storage.ErrDone
	}
	if err := stx.Commit(); err != nil {
		_ = stx.Rollback()
		return err
	}

	t.m.mu.Lock()
	t.m.commitTS = cts
	t.m.liveKeys += delta
	t.m.commits++
	delete(t.m.open, t)
	after := t.m.hooks.AfterCommit
	t.m.mu.Unlock()

	t.mu.Lock()
	t.done = true
	t.writes = nil
	t.commitTS = cts
	t.mu.Unlock()

	if after != nil {
		return after()
	}
	return nil
}

// Rollback drops the write set. A read-write transaction with writes appends
// TxnAbort when the engine implements AbortLogger.
func (t *Tx) Rollback() error {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	had := hasWrites(t.writes) && !t.ro
	id := t.id
	t.done = true
	t.writes = nil
	t.mu.Unlock()
	t.m.drop(t, true)
	if had {
		t.m.logAbort(id)
	}
	return nil
}

func (t *Tx) finish(rollback bool) {
	t.mu.Lock()
	t.done = true
	t.writes = nil
	t.mu.Unlock()
	t.m.mu.Lock()
	delete(t.m.open, t)
	if rollback {
		t.m.rollbacks++
	} else {
		t.m.commits++
	}
	t.m.mu.Unlock()
}

func (t *Tx) touch() error {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return storage.ErrDone
	}
	timeout := t.m.opt.IdleTimeout
	last := t.last
	t.mu.Unlock()
	if timeout > 0 && !last.IsZero() && time.Since(last) > timeout {
		t.mu.Lock()
		if !t.done {
			t.done = true
			t.writes = nil
		}
		t.mu.Unlock()
		t.m.drop(t, true)
		return gerr.New(gerr.ResourceExhausted, "transaction idle timeout")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	t.last = time.Now()
	return nil
}

func (m *Manager) readAt(ks storage.Keyspace, key []byte, snap uint64) ([]byte, error) {
	stx, err := m.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stx.Rollback() }()
	h, ok, err := readHead(stx, ks, key)
	if err != nil {
		return nil, err
	}
	payload, found, err := resolve(stx, ks, key, h, ok, snap)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, storage.ErrNotFound
	}
	return payload, nil
}

func resolve(stx storage.Tx, ks storage.Keyspace, key []byte, h head, ok bool, snap uint64) ([]byte, bool, error) {
	if ok && Visible(h.create, h.delete, snap) {
		return payloadCopy(h.payload), true, nil
	}
	if !ok || (h.create <= snap && h.delete != 0 && h.delete <= snap) {
		return nil, false, nil
	}
	chain, err := loadChain(stx, ks, key)
	if err != nil {
		return nil, false, err
	}
	payload, found := visiblePayload(h, ok, chain, snap)
	return payload, found, nil
}

func validate(stx storage.Tx, snap uint64, writes map[storage.Keyspace]map[string]writeOp) error {
	for ks, ops := range writes {
		for k, op := range ops {
			h, ok, err := readHead(stx, ks, []byte(k))
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if h.create > snap || (h.delete != 0 && h.delete > snap) {
				if op.unique {
					return gerr.New(gerr.ConstraintViolation, "unique key was committed by another transaction")
				}
				return gerr.New(gerr.Conflict, "write conflict")
			}
			if op.unique && !op.del && h.delete == 0 && !op.shadowed {
				return gerr.New(gerr.ConstraintViolation, "unique key already exists")
			}
		}
	}
	return nil
}

func publish(stx storage.Tx, cts uint64, writes map[storage.Keyspace]map[string]writeOp) (int, error) {
	delta := 0
	for ks, ops := range writes {
		for k, op := range ops {
			key := []byte(k)
			h, ok, err := readHead(stx, ks, key)
			if err != nil {
				return 0, err
			}
			wasLive := ok && h.delete == 0
			if op.del {
				if !ok || h.delete != 0 {
					continue
				}
				if err := stx.Put(ks, key, encodeHead(h.create, cts, h.payload)); err != nil {
					return 0, err
				}
				if wasLive {
					delta--
				}
				continue
			}
			if ok {
				if err := archive(stx, ks, key, h, cts); err != nil {
					return 0, err
				}
			}
			if err := stx.Put(ks, key, encodeHead(cts, 0, op.val)); err != nil {
				return 0, err
			}
			if !wasLive {
				delta++
			}
		}
	}
	return delta, nil
}

func archive(stx storage.Tx, ks storage.Keyspace, key []byte, h head, cts uint64) error {
	del := h.delete
	if del == 0 {
		del = cts
	}
	return stx.Put(storage.KSVersion, chainKey(ks, key, h.create), encodeChain(del, h.payload))
}

func (t *Tx) materialize(ks storage.Keyspace) ([][]byte, [][]byte, error) {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return nil, nil, storage.ErrDone
	}
	snap := t.snapshot
	overlay := cloneOps(t.writes[ks])
	t.mu.Unlock()

	stx, err := t.m.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = stx.Rollback() }()

	type item struct {
		k []byte
		h head
	}
	var heads []item
	if err := eachHead(stx, ks, func(key []byte, h head) error {
		heads = append(heads, item{k: key, h: h})
		return nil
	}); err != nil {
		return nil, nil, err
	}
	seen := map[string]struct{}{}
	var base []kv
	for _, it := range heads {
		payload, found, err := resolve(stx, ks, it.k, it.h, true, snap)
		if err != nil {
			return nil, nil, err
		}
		s := string(it.k)
		seen[s] = struct{}{}
		if op, ok := overlay[s]; ok {
			if op.del {
				continue
			}
			base = append(base, kv{k: clone(it.k), v: payloadCopy(op.val)})
			continue
		}
		if !found {
			continue
		}
		base = append(base, kv{k: clone(it.k), v: payload})
	}
	var extra []kv
	for s, op := range overlay {
		if op.del {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		extra = append(extra, kv{k: []byte(s), v: payloadCopy(op.val)})
	}
	sort.Slice(extra, func(i, j int) bool {
		return bytes.Compare(extra[i].k, extra[j].k) < 0
	})
	merged := mergeKV(base, extra)
	keys := make([][]byte, len(merged))
	vals := make([][]byte, len(merged))
	for i, it := range merged {
		keys[i] = it.k
		vals[i] = it.v
	}
	return keys, vals, nil
}

type kv struct {
	k []byte
	v []byte
}

func mergeKV(a, b []kv) []kv {
	out := make([]kv, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if bytes.Compare(a[i].k, b[j].k) <= 0 {
			out = append(out, a[i])
			i++
			continue
		}
		out = append(out, b[j])
		j++
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func cloneWrites(in map[storage.Keyspace]map[string]writeOp) map[storage.Keyspace]map[string]writeOp {
	out := make(map[storage.Keyspace]map[string]writeOp, len(in))
	for ks, ops := range in {
		out[ks] = cloneOps(ops)
	}
	return out
}

func cloneOps(in map[string]writeOp) map[string]writeOp {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]writeOp, len(in))
	for k, op := range in {
		op.val = clone(op.val)
		out[k] = op
	}
	return out
}

func hasWrites(w map[storage.Keyspace]map[string]writeOp) bool {
	for _, ops := range w {
		if len(ops) > 0 {
			return true
		}
	}
	return false
}

func writeCount(w map[storage.Keyspace]map[string]writeOp) int {
	n := 0
	for _, ops := range w {
		n += len(ops)
	}
	return n
}

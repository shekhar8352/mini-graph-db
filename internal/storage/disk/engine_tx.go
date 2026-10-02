package disk

import (
	"errors"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

func (t *diskTx) Get(ks storage.Keyspace, key []byte) ([]byte, error) {
	e := t.eng
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.done {
		return nil, storage.ErrDone
	}
	if e.broken != nil {
		return nil, e.broken
	}
	if op, ok := t.writes[ks][string(key)]; ok {
		if op.del {
			return nil, storage.ErrNotFound
		}
		return cloneBytes(op.val), nil
	}
	return e.readGen(t.gen, ks, key)
}

func (e *Engine) readGen(gen uint64, ks storage.Keyspace, key []byte) ([]byte, error) {
	if rec, ok := e.oldestUndo(gen, ks, key); ok {
		if !rec.existed {
			return nil, storage.ErrNotFound
		}
		return cloneBytes(rec.val), nil
	}
	tree := e.trees[ks]
	if tree == nil {
		return nil, gerr.Newf(gerr.InvalidArgument, "unknown keyspace %q", string(ks))
	}
	val, err := tree.Get(key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(val), nil
}

func (t *diskTx) Put(ks storage.Keyspace, key, val []byte) error {
	return t.stage(ks, key, val, false)
}

func (t *diskTx) Delete(ks storage.Keyspace, key []byte) error {
	return t.stage(ks, key, nil, true)
}

func (t *diskTx) stage(ks storage.Keyspace, key, val []byte, del bool) error {
	e := t.eng
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	if t.ro {
		return storage.ErrReadOnly
	}
	if e.broken != nil {
		return e.broken
	}
	if _, err := rootSlot(ks); err != nil {
		return err
	}
	if t.writes[ks] == nil {
		t.writes[ks] = map[string]writeOp{}
	}
	t.writes[ks][string(key)] = writeOp{val: cloneBytes(val), del: del}
	return nil
}

func (t *diskTx) Commit() error {
	e := t.eng
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return storage.ErrClosed
	}
	if t.done {
		e.mu.Unlock()
		return storage.ErrDone
	}
	if e.broken != nil {
		err := e.broken
		e.mu.Unlock()
		return err
	}
	if t.ro || !hasOps(t.writes) {
		t.finish(e)
		e.commits++
		e.mu.Unlock()
		return nil
	}
	if h := e.hooks.BeforeCommit; h != nil {
		if err := h(); err != nil {
			e.mu.Unlock()
			return err
		}
	}
	ops := collectOps(t.writes)
	e.nextTxn++
	txnID := e.nextTxn
	if err := e.file.Hold(); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.applyOps(ops); err != nil {
		e.file.Discard()
		e.broken = err
		e.mu.Unlock()
		return err
	}
	images, err := e.pageImages()
	if err != nil {
		e.file.Discard()
		e.broken = err
		e.mu.Unlock()
		return err
	}
	if err := e.logPages(txnID, images); err != nil {
		e.file.Discard()
		e.broken = err
		e.mu.Unlock()
		return err
	}
	if err := e.wal.Sync(); err != nil {
		e.file.Discard()
		e.broken = err
		e.mu.Unlock()
		return err
	}
	if e.stopAfterSync {
		e.mu.Unlock()
		return errStopAfterSync
	}
	if err := e.publish(images); err != nil {
		e.broken = err
		e.mu.Unlock()
		return err
	}
	t.finish(e)
	e.commits++
	var after error
	if h := e.hooks.AfterCommit; h != nil {
		after = h()
	}
	e.gcUndo()
	e.mu.Unlock()
	e.kickCheckpoint()
	return after
}

func (t *diskTx) Rollback() error {
	e := t.eng
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.done {
		return storage.ErrDone
	}
	t.finish(e)
	e.rollback++
	e.gcUndo()
	return nil
}

func (e *Engine) applyOps(ops []op) error {
	newGen := e.gen + 1
	for _, op := range ops {
		tree := e.trees[op.ks]
		old, err := tree.Get(op.key)
		existed := err == nil
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		e.noteUndo(newGen, op.ks, op.key, old, existed)
		if op.del {
			if err := tree.Delete(op.key); err != nil {
				return err
			}
			if existed {
				e.keys--
			}
			continue
		}
		if err := tree.Put(op.key, op.val); err != nil {
			return err
		}
		if !existed {
			e.keys++
		}
	}
	e.gen = newGen
	return nil
}

func (e *Engine) pageImages() (map[PageID][]byte, error) {
	images := e.file.StagedImages()
	if images == nil {
		images = map[PageID][]byte{}
	}
	dirty, err := e.pool.DirtyImages()
	if err != nil {
		return nil, err
	}
	for id, img := range dirty {
		images[id] = img
	}
	images[0] = e.file.HeaderImage()
	return images, nil
}

func (e *Engine) logPages(txnID uint64, images map[PageID][]byte) error {
	if _, err := e.wal.Append(wal.TypeTxnBegin, txnID, nil); err != nil {
		return err
	}
	ids := make([]PageID, 0, len(images))
	for id := range images {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ids = append(ids, 0)
	for _, id := range ids {
		img := cloneBytes(images[id])
		setPageLSN(img, uint64(e.wal.End()))
		seal(img)
		images[id] = img
		payload, err := wal.EncodePageWrite(uint64(id), img)
		if err != nil {
			return err
		}
		if _, err := e.wal.Append(wal.TypePageWrite, txnID, payload); err != nil {
			return err
		}
	}
	_, err := e.wal.Append(wal.TypeTxnCommit, txnID, nil)
	return err
}

func (e *Engine) publish(images map[PageID][]byte) error {
	_ = e.file.ReleaseHold()
	ids := make([]PageID, 0, len(images))
	for id := range images {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if err := e.file.WriteImageAt(images[id]); err != nil {
			return err
		}
	}
	if err := e.file.Sync(); err != nil {
		return err
	}
	if img := images[0]; img != nil {
		if err := e.file.WriteImageAt(img); err != nil {
			return err
		}
	}
	if err := e.file.Sync(); err != nil {
		return err
	}
	return e.pool.Invalidate()
}

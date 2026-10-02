package disk

import (
	"bytes"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

const (
	dirFwd  = 1
	dirBack = -1
)

type ovItem struct {
	key     []byte
	val     []byte
	present bool
}

// engCursor walks one keyspace. It holds the tree read lock from the first
// successful seek until Close so a commit cannot change the leaf chain.
type engCursor struct {
	tx      *diskTx
	ks      storage.Keyspace
	bcur    *Cursor
	overlay []ovItem
	oi      int
	bValid  bool
	bKey    []byte
	bVal    []byte
	key     []byte
	val     []byte
	valid   bool
	dir     int
	closed  bool
	err     error
}

func (t *diskTx) Cursor(ks storage.Keyspace) (storage.Cursor, error) {
	e := t.eng
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.done {
		return nil, storage.ErrDone
	}
	if _, err := rootSlot(ks); err != nil {
		return nil, err
	}
	return &engCursor{tx: t, ks: ks}, nil
}

func (c *engCursor) Seek(target []byte) bool {
	if !c.prepare() {
		return false
	}
	if target == nil {
		_ = c.bcur.Seek(nil)
		c.loadB()
		c.oi = 0
	} else {
		_ = c.bcur.Seek(target)
		c.loadB()
		c.oi = sort.Search(len(c.overlay), func(i int) bool {
			return bytes.Compare(c.overlay[i].key, target) >= 0
		})
	}
	c.dir = dirFwd
	return c.advanceForward()
}

func (c *engCursor) SeekReverse(target []byte) bool {
	if !c.prepare() {
		return false
	}
	if target == nil {
		_ = c.bcur.SeekReverse(nil)
		c.loadB()
		c.oi = len(c.overlay) - 1
	} else {
		_ = c.bcur.SeekReverse(target)
		c.loadB()
		c.oi = sort.Search(len(c.overlay), func(i int) bool {
			return bytes.Compare(c.overlay[i].key, target) > 0
		}) - 1
	}
	c.dir = dirBack
	return c.advanceBackward()
}

func (c *engCursor) Next() bool {
	if c.closed || c.tx.flag.Load() || !c.valid || c.err != nil {
		c.valid = false
		return false
	}
	if c.dir != dirFwd {
		cur := cloneBytes(c.key)
		c.repositionAfter(cur)
		c.dir = dirFwd
	}
	return c.advanceForward()
}

func (c *engCursor) Prev() bool {
	if c.closed || c.tx.flag.Load() || !c.valid || c.err != nil {
		c.valid = false
		return false
	}
	if c.dir != dirBack {
		cur := cloneBytes(c.key)
		c.repositionBefore(cur)
		c.dir = dirBack
	}
	return c.advanceBackward()
}

func (c *engCursor) Key() []byte {
	if !c.valid {
		return nil
	}
	return cloneBytes(c.key)
}

func (c *engCursor) Value() []byte {
	if !c.valid {
		return nil
	}
	return cloneBytes(c.val)
}

func (c *engCursor) Valid() bool { return c.valid && !c.closed && !c.tx.flag.Load() }

func (c *engCursor) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	c.valid = false
	c.release()
	return nil
}

func (c *engCursor) prepare() bool {
	if c.closed || c.tx.flag.Load() {
		c.release()
		c.valid = false
		return false
	}
	c.release()
	e := c.tx.eng
	e.mu.Lock()
	if c.tx.done || c.closed {
		e.mu.Unlock()
		c.valid = false
		return false
	}
	c.overlay = e.overlay(c.tx, c.ks)
	tree := e.trees[c.ks]
	cur, err := tree.Cursor()
	if err != nil {
		e.mu.Unlock()
		c.err = err
		return false
	}
	// Seek acquires the tree read lock before the engine lock is released.
	_ = cur.Seek(nil)
	if cur.Err() != nil {
		err = cur.Err()
		_ = cur.Close()
		e.mu.Unlock()
		c.err = err
		return false
	}
	c.bcur = cur
	e.mu.Unlock()
	return true
}

func (c *engCursor) release() {
	if c.bcur != nil {
		_ = c.bcur.Close()
		c.bcur = nil
	}
	c.bValid = false
	c.valid = false
}

func (c *engCursor) loadB() {
	if c.bcur == nil || !c.bcur.Valid() {
		c.bValid = false
		if c.bcur != nil && c.bcur.Err() != nil {
			c.err = c.bcur.Err()
		}
		return
	}
	c.bValid = true
	c.bKey = c.bcur.Key()
	c.bVal = c.bcur.Value()
}

func (c *engCursor) consumeB() {
	if c.bcur == nil || !c.bcur.Next() {
		c.bValid = false
		if c.bcur != nil && c.bcur.Err() != nil {
			c.err = c.bcur.Err()
		}
		return
	}
	c.loadB()
}

func (c *engCursor) retreatB() {
	if c.bcur == nil || !c.bcur.Prev() {
		c.bValid = false
		if c.bcur != nil && c.bcur.Err() != nil {
			c.err = c.bcur.Err()
		}
		return
	}
	c.loadB()
}

func (c *engCursor) publish(k, v []byte) {
	c.key = cloneBytes(k)
	c.val = cloneBytes(v)
	c.valid = true
}

func (c *engCursor) advanceForward() bool {
	for {
		if c.err != nil {
			c.valid = false
			return false
		}
		hasB := c.bValid
		hasO := c.oi >= 0 && c.oi < len(c.overlay)
		if !hasB && !hasO {
			c.valid = false
			return false
		}
		if hasO && (!hasB || bytes.Compare(c.overlay[c.oi].key, c.bKey) < 0) {
			item := c.overlay[c.oi]
			c.oi++
			if !item.present {
				continue
			}
			c.publish(item.key, item.val)
			return true
		}
		if hasB && (!hasO || bytes.Compare(c.bKey, c.overlay[c.oi].key) < 0) {
			c.publish(c.bKey, c.bVal)
			c.consumeB()
			return true
		}
		item := c.overlay[c.oi]
		c.oi++
		c.consumeB()
		if !item.present {
			continue
		}
		c.publish(item.key, item.val)
		return true
	}
}

func (c *engCursor) advanceBackward() bool {
	for {
		if c.err != nil {
			c.valid = false
			return false
		}
		hasB := c.bValid
		hasO := c.oi >= 0 && c.oi < len(c.overlay)
		if !hasB && !hasO {
			c.valid = false
			return false
		}
		if hasO && (!hasB || bytes.Compare(c.overlay[c.oi].key, c.bKey) > 0) {
			item := c.overlay[c.oi]
			c.oi--
			if !item.present {
				continue
			}
			c.publish(item.key, item.val)
			return true
		}
		if hasB && (!hasO || bytes.Compare(c.bKey, c.overlay[c.oi].key) > 0) {
			c.publish(c.bKey, c.bVal)
			c.retreatB()
			return true
		}
		item := c.overlay[c.oi]
		c.oi--
		c.retreatB()
		if !item.present {
			continue
		}
		c.publish(item.key, item.val)
		return true
	}
}

func (c *engCursor) repositionAfter(key []byte) {
	if !c.bcur.Seek(key) {
		c.loadB()
	} else {
		c.loadB()
		if c.bValid && bytes.Compare(c.bKey, key) <= 0 {
			c.consumeB()
		}
	}
	c.oi = sort.Search(len(c.overlay), func(i int) bool {
		return bytes.Compare(c.overlay[i].key, key) > 0
	})
}

func (c *engCursor) repositionBefore(key []byte) {
	if !c.bcur.Seek(key) {
		if !c.bcur.SeekReverse(nil) {
			c.bValid = false
		} else {
			c.loadB()
		}
	} else if !c.bcur.Prev() {
		c.bValid = false
	} else {
		c.loadB()
	}
	if c.bValid && bytes.Compare(c.bKey, key) >= 0 {
		c.bValid = false
	}
	c.oi = sort.Search(len(c.overlay), func(i int) bool {
		return bytes.Compare(c.overlay[i].key, key) >= 0
	}) - 1
}

func (e *Engine) overlay(tx *diskTx, ks storage.Keyspace) []ovItem {
	items := map[string]ovItem{}
	for uk, recs := range e.undo {
		if uk.ks != ks {
			continue
		}
		var best undoRec
		found := false
		for _, rec := range recs {
			if rec.gen <= tx.gen {
				continue
			}
			if !found || rec.gen < best.gen {
				best = rec
				found = true
			}
		}
		if !found {
			continue
		}
		items[uk.key] = ovItem{key: []byte(uk.key), val: cloneBytes(best.val), present: best.existed}
	}
	for k, op := range tx.writes[ks] {
		if op.del {
			items[k] = ovItem{key: []byte(k), present: false}
			continue
		}
		items[k] = ovItem{key: []byte(k), val: cloneBytes(op.val), present: true}
	}
	out := make([]ovItem, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].key, out[j].key) < 0 })
	return out
}

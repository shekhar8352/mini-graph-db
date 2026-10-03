package txn

import (
	"bytes"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

type cursor struct {
	tx     *Tx
	ks     storage.Keyspace
	keys   [][]byte
	vals   [][]byte
	idx    int
	closed bool
	err    error
}

func (c *cursor) Seek(target []byte) bool {
	if !c.usable() {
		c.idx = -1
		return false
	}
	keys, vals, err := c.tx.materialize(c.ks)
	if err != nil {
		c.err = err
		c.idx = -1
		return false
	}
	c.keys, c.vals = keys, vals
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

func (c *cursor) SeekReverse(target []byte) bool {
	if !c.usable() {
		c.idx = -1
		return false
	}
	keys, vals, err := c.tx.materialize(c.ks)
	if err != nil {
		c.err = err
		c.idx = -1
		return false
	}
	c.keys, c.vals = keys, vals
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

func (c *cursor) Next() bool {
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

func (c *cursor) Prev() bool {
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

func (c *cursor) Key() []byte {
	if !c.positioned() {
		return nil
	}
	return clone(c.keys[c.idx])
}

func (c *cursor) Value() []byte {
	if !c.positioned() {
		return nil
	}
	return payloadCopy(c.vals[c.idx])
}

func (c *cursor) Valid() bool { return c.positioned() }

func (c *cursor) Close() error {
	c.closed = true
	c.idx = -1
	err := c.err
	c.err = nil
	return err
}

func (c *cursor) usable() bool {
	if c.closed || c.err != nil {
		return false
	}
	c.tx.mu.Lock()
	done := c.tx.done
	c.tx.mu.Unlock()
	return !done
}

func (c *cursor) positioned() bool {
	return c.usable() && c.idx >= 0 && c.idx < len(c.keys)
}

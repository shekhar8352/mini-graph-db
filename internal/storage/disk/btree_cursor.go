package disk

import (
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

// Cursor walks one tree in key order. It holds the tree's read lock until
// Close. Seek(nil) lands on the first key. Seek of an empty slice lands on
// the first key greater than or equal to the empty key. SeekReverse(nil)
// lands on the last key.
type Cursor struct {
	tree   *Tree
	leaf   PageID
	idx    int
	key    []byte
	val    []byte
	valid  bool
	locked bool
	err    error
}

var _ storage.Cursor = (*Cursor)(nil)

// Cursor returns a cursor that is not yet positioned.
func (t *Tree) Cursor() (*Cursor, error) {
	if t == nil {
		return nil, gerr.New(gerr.InvalidArgument, "tree is nil")
	}
	return &Cursor{tree: t}, nil
}

// Seek positions the cursor on the first key greater than or equal to target.
// A nil target selects the first key.
func (c *Cursor) Seek(target []byte) bool {
	c.ensureLock()
	c.valid = false
	c.err = nil
	var err error
	if target == nil {
		err = c.seekFirst()
	} else {
		err = c.seekGE(target)
	}
	if err != nil {
		c.err = err
		c.valid = false
		return false
	}
	return c.valid
}

// SeekReverse positions the cursor on the last key less than or equal to target.
// A nil target selects the last key.
func (c *Cursor) SeekReverse(target []byte) bool {
	c.ensureLock()
	c.valid = false
	c.err = nil
	var err error
	if target == nil {
		err = c.seekLast()
	} else {
		err = c.seekLE(target)
	}
	if err != nil {
		c.err = err
		c.valid = false
		return false
	}
	return c.valid
}

// Next advances one key. It returns false at the end.
func (c *Cursor) Next() bool {
	if c.err != nil || !c.valid {
		return false
	}
	leaf, err := c.tree.loadLeaf(c.leaf)
	if err != nil {
		c.fail(err)
		return false
	}
	if c.idx+1 < len(leaf.cells) {
		if err := c.position(leaf, c.idx+1); err != nil {
			c.fail(err)
			return false
		}
		return true
	}
	if leaf.right == 0 {
		c.valid = false
		return false
	}
	next, err := c.tree.loadLeaf(leaf.right)
	if err != nil {
		c.fail(err)
		return false
	}
	if len(next.cells) == 0 {
		c.fail(gerrCorrupt("leaf page %d is empty", next.id))
		return false
	}
	if err := c.position(next, 0); err != nil {
		c.fail(err)
		return false
	}
	return true
}

// Prev steps back one key. It returns false at the start.
func (c *Cursor) Prev() bool {
	if c.err != nil || !c.valid {
		return false
	}
	leaf, err := c.tree.loadLeaf(c.leaf)
	if err != nil {
		c.fail(err)
		return false
	}
	if c.idx > 0 {
		if err := c.position(leaf, c.idx-1); err != nil {
			c.fail(err)
			return false
		}
		return true
	}
	if leaf.left == 0 {
		c.valid = false
		return false
	}
	prev, err := c.tree.loadLeaf(leaf.left)
	if err != nil {
		c.fail(err)
		return false
	}
	if len(prev.cells) == 0 {
		c.fail(gerrCorrupt("leaf page %d is empty", prev.id))
		return false
	}
	if err := c.position(prev, len(prev.cells)-1); err != nil {
		c.fail(err)
		return false
	}
	return true
}

// Key returns a copy of the current key. It is nil when the cursor is invalid.
func (c *Cursor) Key() []byte {
	if !c.valid {
		return nil
	}
	return append([]byte(nil), c.key...)
}

// Value returns a copy of the current value. It is nil when the cursor is invalid.
func (c *Cursor) Value() []byte {
	if !c.valid {
		return nil
	}
	return append([]byte(nil), c.val...)
}

// Valid reports whether the cursor points at a key.
func (c *Cursor) Valid() bool { return c.valid }

// Err returns the first error from a positioning call.
func (c *Cursor) Err() error { return c.err }

// Close releases the read lock. A second Close is a no-op.
func (c *Cursor) Close() error {
	if c == nil || !c.locked {
		return nil
	}
	c.tree.mu.RUnlock()
	c.locked = false
	c.valid = false
	return nil
}

func (c *Cursor) ensureLock() {
	if !c.locked {
		c.tree.mu.RLock()
		c.locked = true
	}
}

func (c *Cursor) fail(err error) {
	c.err = err
	c.valid = false
}

func (c *Cursor) seekFirst() error {
	root, err := c.tree.root()
	if err != nil || root == 0 {
		return err
	}
	leaf, err := c.tree.edgeLeaf(root, true)
	if err != nil || len(leaf.cells) == 0 {
		return err
	}
	return c.position(leaf, 0)
}

func (c *Cursor) seekLast() error {
	root, err := c.tree.root()
	if err != nil || root == 0 {
		return err
	}
	leaf, err := c.tree.edgeLeaf(root, false)
	if err != nil || len(leaf.cells) == 0 {
		return err
	}
	return c.position(leaf, len(leaf.cells)-1)
}

func (c *Cursor) seekGE(target []byte) error {
	root, err := c.tree.root()
	if err != nil || root == 0 {
		return err
	}
	leaf, idx, _, err := c.tree.descend(root, target)
	if err != nil {
		return err
	}
	if idx >= len(leaf.cells) {
		if leaf.right == 0 {
			return nil
		}
		leaf, err = c.tree.loadLeaf(leaf.right)
		if err != nil {
			return err
		}
		if len(leaf.cells) == 0 {
			return gerrCorrupt("leaf page %d is empty", leaf.id)
		}
		idx = 0
	}
	return c.position(leaf, idx)
}

func (c *Cursor) seekLE(target []byte) error {
	root, err := c.tree.root()
	if err != nil || root == 0 {
		return err
	}
	leaf, idx, _, err := c.tree.descend(root, target)
	if err != nil {
		return err
	}
	if len(leaf.cells) == 0 {
		return nil
	}
	if idx >= len(leaf.cells) {
		idx = len(leaf.cells) - 1
	} else if bytesCompare(leaf.cells[idx].key, target) > 0 {
		if idx == 0 {
			if leaf.left == 0 {
				return nil
			}
			leaf, err = c.tree.loadLeaf(leaf.left)
			if err != nil {
				return err
			}
			if len(leaf.cells) == 0 {
				return gerrCorrupt("leaf page %d is empty", leaf.id)
			}
			idx = len(leaf.cells) - 1
		} else {
			idx--
		}
	}
	return c.position(leaf, idx)
}

func (c *Cursor) position(leaf leafPage, idx int) error {
	if idx < 0 || idx >= len(leaf.cells) {
		c.valid = false
		return nil
	}
	val, err := c.tree.cellValue(leaf.cells[idx])
	if err != nil {
		return err
	}
	c.leaf = leaf.id
	c.idx = idx
	c.key = leaf.cells[idx].key
	c.val = val
	c.valid = true
	return nil
}

func (t *Tree) edgeLeaf(id PageID, left bool) (leafPage, error) {
	for hop := 0; hop < maxTreeHeight; hop++ {
		typ, err := t.pageType(id)
		if err != nil {
			return leafPage{}, err
		}
		if typ == TypeTreeLeaf {
			return t.loadLeaf(id)
		}
		if typ != TypeTreeInternal {
			return leafPage{}, gerrCorrupt("page %d has type %d", id, typ)
		}
		node, err := t.loadInternal(id)
		if err != nil {
			return leafPage{}, err
		}
		if len(node.children) == 0 {
			return leafPage{}, gerrCorrupt("internal page %d has no children", id)
		}
		if left {
			id = node.children[0]
		} else {
			id = node.children[len(node.children)-1]
		}
	}
	return leafPage{}, gerrCorrupt("tree is deeper than the height limit")
}

func bytesCompare(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func gerrCorrupt(format string, args ...any) error {
	return corruptionf(format, args...)
}

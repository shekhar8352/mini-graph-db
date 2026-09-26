package disk

import "github.com/shekhar8352/mini-graph-db/internal/gerr"

// verify checks key order, separators, sibling links, and a uniform leaf depth.
// It is for tests. Callers must not already hold the tree lock.
func (t *Tree) verify() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	root, err := t.root()
	if err != nil || root == 0 {
		return err
	}
	var leaves []PageID
	var depth int
	var haveDepth bool
	if err := t.verifyPage(root, nil, nil, true, 0, &leaves, &depth, &haveDepth); err != nil {
		return err
	}
	if len(leaves) == 0 {
		return gerr.New(gerr.Corruption, "tree root has no leaves")
	}
	for i, id := range leaves {
		leaf, err := t.loadLeaf(id)
		if err != nil {
			return err
		}
		var wantLeft PageID
		if i > 0 {
			wantLeft = leaves[i-1]
		}
		var wantRight PageID
		if i+1 < len(leaves) {
			wantRight = leaves[i+1]
		}
		if leaf.left != wantLeft || leaf.right != wantRight {
			return gerr.Newf(gerr.Corruption, "leaf %d siblings left=%d right=%d, want %d and %d", id, leaf.left, leaf.right, wantLeft, wantRight)
		}
	}
	return nil
}

func (t *Tree) verifyPage(id PageID, lo, hi []byte, loExact bool, depth int, leaves *[]PageID, leafDepth *int, haveDepth *bool) error {
	if depth > maxTreeHeight {
		return gerr.New(gerr.Corruption, "tree is deeper than the height limit")
	}
	typ, err := t.pageType(id)
	if err != nil {
		return err
	}
	if typ == TypeTreeLeaf {
		if *haveDepth && *leafDepth != depth {
			return gerr.Newf(gerr.Corruption, "leaf %d is at depth %d, want %d", id, depth, *leafDepth)
		}
		*haveDepth = true
		*leafDepth = depth
		*leaves = append(*leaves, id)
		leaf, err := t.loadLeaf(id)
		if err != nil {
			return err
		}
		if len(leaf.cells) == 0 {
			return gerr.Newf(gerr.Corruption, "leaf page %d is empty", id)
		}
		if lo != nil {
			cmp := bytesCompare(leaf.cells[0].key, lo)
			if loExact && cmp != 0 {
				return gerr.Newf(gerr.Corruption, "leaf %d first key does not match its separator", id)
			}
			if cmp < 0 {
				return gerr.Newf(gerr.Corruption, "leaf %d has a key below its lower bound", id)
			}
		}
		for i, c := range leaf.cells {
			if i > 0 && bytesCompare(leaf.cells[i-1].key, c.key) >= 0 {
				return gerr.Newf(gerr.Corruption, "leaf %d keys are not strictly increasing", id)
			}
			if hi != nil && bytesCompare(c.key, hi) >= 0 {
				return gerr.Newf(gerr.Corruption, "leaf %d has a key at or above its upper bound", id)
			}
		}
		return nil
	}
	if typ != TypeTreeInternal {
		return gerr.Newf(gerr.Corruption, "page %d has type %d in the tree", id, typ)
	}
	node, err := t.loadInternal(id)
	if err != nil {
		return err
	}
	if len(node.children) != len(node.keys)+1 || len(node.keys) == 0 {
		return gerr.Newf(gerr.Corruption, "internal page %d has %d keys and %d children", id, len(node.keys), len(node.children))
	}
	for i := 1; i < len(node.keys); i++ {
		if bytesCompare(node.keys[i-1], node.keys[i]) >= 0 {
			return gerr.Newf(gerr.Corruption, "internal page %d keys are not strictly increasing", id)
		}
	}
	if lo != nil && bytesCompare(node.keys[0], lo) < 0 {
		return gerr.Newf(gerr.Corruption, "internal page %d is below its lower bound", id)
	}
	if hi != nil && bytesCompare(node.keys[len(node.keys)-1], hi) >= 0 {
		return gerr.Newf(gerr.Corruption, "internal page %d is at or above its upper bound", id)
	}
	for i, child := range node.children {
		var childLo, childHi []byte
		exact := false
		if i == 0 {
			childLo = lo
			exact = loExact
		} else {
			childLo = node.keys[i-1]
			exact = true
		}
		if i == len(node.keys) {
			childHi = hi
		} else {
			childHi = node.keys[i]
		}
		if err := t.verifyPage(child, childLo, childHi, exact, depth+1, leaves, leafDepth, haveDepth); err != nil {
			return err
		}
	}
	return nil
}

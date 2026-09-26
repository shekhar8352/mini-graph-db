package disk

import "github.com/shekhar8352/mini-graph-db/internal/gerr"

// repair fixes the child at childIdx after a deletion in its subtree.
// A non-root page below half full borrows from a sibling or merges with one.
// Merging can empty the parent. An internal root with no keys is replaced
// by its only child.
func (t *Tree) repair(parentID PageID, childIdx int, parentIsRoot bool) error {
	parent, err := t.loadInternal(parentID)
	if err != nil {
		return err
	}
	if childIdx < 0 || childIdx >= len(parent.children) {
		return gerr.Newf(gerr.Corruption, "page %d has no child %d", parentID, childIdx)
	}
	under, err := t.pageUnderfull(parent.children[childIdx])
	if err != nil {
		return err
	}
	surviving := childIdx
	if under {
		surviving, err = t.rebalance(parentID, childIdx)
		if err != nil {
			return err
		}
		parent, err = t.loadInternal(parentID)
		if err != nil {
			return err
		}
	}
	if parentIsRoot && len(parent.keys) == 0 {
		return t.collapseRoot(parent)
	}
	if surviving <= 0 || surviving >= len(parent.children) {
		return nil
	}
	return t.syncSeparator(parentID, surviving)
}

func (t *Tree) rebalance(parentID PageID, idx int) (int, error) {
	parent, err := t.loadInternal(parentID)
	if err != nil {
		return 0, err
	}
	if idx > 0 {
		ok, err := t.tryMerge(parentID, idx-1)
		if err != nil {
			return 0, err
		}
		if ok {
			return idx - 1, nil
		}
	}
	parent, err = t.loadInternal(parentID)
	if err != nil {
		return 0, err
	}
	if idx+1 < len(parent.children) {
		ok, err := t.tryMerge(parentID, idx)
		if err != nil {
			return 0, err
		}
		if ok {
			return idx, nil
		}
	}
	for {
		under, err := t.pageUnderfull(parent.children[idx])
		if err != nil {
			return 0, err
		}
		if !under {
			return idx, nil
		}
		moved := false
		if idx > 0 {
			moved, err = t.tryBorrow(parentID, idx-1, false)
			if err != nil {
				return 0, err
			}
		}
		if !moved && idx+1 < len(parent.children) {
			moved, err = t.tryBorrow(parentID, idx, true)
			if err != nil {
				return 0, err
			}
		}
		if !moved {
			return idx, nil
		}
		parent, err = t.loadInternal(parentID)
		if err != nil {
			return 0, err
		}
	}
}

func (t *Tree) tryMerge(parentID PageID, leftIdx int) (bool, error) {
	parent, err := t.loadInternal(parentID)
	if err != nil {
		return false, err
	}
	if leftIdx < 0 || leftIdx+1 >= len(parent.children) {
		return false, nil
	}
	leftID := parent.children[leftIdx]
	rightID := parent.children[leftIdx+1]
	typ, err := t.pageType(leftID)
	if err != nil {
		return false, err
	}
	other, err := t.pageType(rightID)
	if err != nil {
		return false, err
	}
	if typ != other {
		return false, gerr.Newf(gerr.Corruption, "sibling pages %d and %d have types %d and %d", leftID, rightID, typ, other)
	}
	if typ == TypeTreeLeaf {
		return t.mergeLeaves(parent, leftIdx)
	}
	return t.mergeInternal(parent, leftIdx)
}

func (t *Tree) mergeLeaves(parent internalPage, leftIdx int) (bool, error) {
	left, err := t.loadLeaf(parent.children[leftIdx])
	if err != nil {
		return false, err
	}
	right, err := t.loadLeaf(parent.children[leftIdx+1])
	if err != nil {
		return false, err
	}
	combined := make([]leafCell, 0, len(left.cells)+len(right.cells))
	combined = append(combined, left.cells...)
	combined = append(combined, right.cells...)
	if !leafFits(t.payload, combined) {
		return false, nil
	}
	left.cells = combined
	left.right = right.right
	if err := t.storeLeaf(left); err != nil {
		return false, err
	}
	if right.right != 0 {
		sib, err := t.loadLeaf(right.right)
		if err != nil {
			return false, err
		}
		sib.left = left.id
		if err := t.storeLeaf(sib); err != nil {
			return false, err
		}
	}
	parent.keys = deleteAt(parent.keys, leftIdx)
	parent.children = deleteAt(parent.children, leftIdx+1)
	if err := t.storeInternal(parent); err != nil {
		return false, err
	}
	if err := t.freePage(right.id); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Tree) mergeInternal(parent internalPage, leftIdx int) (bool, error) {
	left, err := t.loadInternal(parent.children[leftIdx])
	if err != nil {
		return false, err
	}
	right, err := t.loadInternal(parent.children[leftIdx+1])
	if err != nil {
		return false, err
	}
	keys := make([][]byte, 0, len(left.keys)+1+len(right.keys))
	keys = append(keys, left.keys...)
	keys = append(keys, append([]byte(nil), parent.keys[leftIdx]...))
	keys = append(keys, right.keys...)
	if !internalFits(t.payload, keys) {
		return false, nil
	}
	children := make([]PageID, 0, len(left.children)+len(right.children))
	children = append(children, left.children...)
	children = append(children, right.children...)
	left.keys = keys
	left.children = children
	if err := t.storeInternal(left); err != nil {
		return false, err
	}
	parent.keys = deleteAt(parent.keys, leftIdx)
	parent.children = deleteAt(parent.children, leftIdx+1)
	if err := t.storeInternal(parent); err != nil {
		return false, err
	}
	if err := t.freePage(right.id); err != nil {
		return false, err
	}
	return true, nil
}

// tryBorrow moves one key across the separator between children leftIdx and
// leftIdx+1. fromRight is true when the left child is the one that needs a key.
func (t *Tree) tryBorrow(parentID PageID, leftIdx int, fromRight bool) (bool, error) {
	parent, err := t.loadInternal(parentID)
	if err != nil {
		return false, err
	}
	if leftIdx < 0 || leftIdx+1 >= len(parent.children) {
		return false, nil
	}
	typ, err := t.pageType(parent.children[leftIdx])
	if err != nil {
		return false, err
	}
	if typ == TypeTreeLeaf {
		return t.borrowLeaf(parent, leftIdx, fromRight)
	}
	return t.borrowInternal(parent, leftIdx, fromRight)
}

func (t *Tree) borrowLeaf(parent internalPage, leftIdx int, fromRight bool) (bool, error) {
	left, err := t.loadLeaf(parent.children[leftIdx])
	if err != nil {
		return false, err
	}
	right, err := t.loadLeaf(parent.children[leftIdx+1])
	if err != nil {
		return false, err
	}
	if fromRight {
		if len(right.cells) < 2 {
			return false, nil
		}
		moved := right.cells[0]
		trial := append(append([]leafCell{}, left.cells...), moved)
		if !leafFits(t.payload, trial) {
			return false, nil
		}
		left.cells = trial
		right.cells = append([]leafCell{}, right.cells[1:]...)
		parent.keys[leftIdx] = append([]byte(nil), right.cells[0].key...)
	} else {
		if len(left.cells) < 2 {
			return false, nil
		}
		moved := left.cells[len(left.cells)-1]
		trial := append([]leafCell{moved}, right.cells...)
		if !leafFits(t.payload, trial) {
			return false, nil
		}
		left.cells = append([]leafCell{}, left.cells[:len(left.cells)-1]...)
		right.cells = trial
		parent.keys[leftIdx] = append([]byte(nil), right.cells[0].key...)
	}
	if err := t.storeLeaf(left); err != nil {
		return false, err
	}
	if err := t.storeLeaf(right); err != nil {
		return false, err
	}
	if err := t.storeInternal(parent); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Tree) borrowInternal(parent internalPage, leftIdx int, fromRight bool) (bool, error) {
	left, err := t.loadInternal(parent.children[leftIdx])
	if err != nil {
		return false, err
	}
	right, err := t.loadInternal(parent.children[leftIdx+1])
	if err != nil {
		return false, err
	}
	if fromRight {
		if len(right.keys) < 2 {
			return false, nil
		}
		keys := append(append([][]byte{}, left.keys...), append([]byte(nil), parent.keys[leftIdx]...))
		if !internalFits(t.payload, keys) {
			return false, nil
		}
		left.keys = keys
		left.children = append(append([]PageID{}, left.children...), right.children[0])
		parent.keys[leftIdx] = append([]byte(nil), right.keys[0]...)
		right.keys = append([][]byte{}, right.keys[1:]...)
		right.children = append([]PageID{}, right.children[1:]...)
	} else {
		if len(left.keys) < 2 {
			return false, nil
		}
		sep := append([]byte(nil), parent.keys[leftIdx]...)
		movedKey := append([]byte(nil), left.keys[len(left.keys)-1]...)
		movedChild := left.children[len(left.children)-1]
		keys := append([][]byte{sep}, right.keys...)
		if !internalFits(t.payload, keys) {
			return false, nil
		}
		left.keys = append([][]byte{}, left.keys[:len(left.keys)-1]...)
		left.children = append([]PageID{}, left.children[:len(left.children)-1]...)
		right.keys = keys
		right.children = append([]PageID{movedChild}, right.children...)
		parent.keys[leftIdx] = movedKey
	}
	if len(left.children) != len(left.keys)+1 || len(right.children) != len(right.keys)+1 {
		return false, gerr.New(gerr.Corruption, "borrow produced an unbalanced internal page")
	}
	if err := t.storeInternal(left); err != nil {
		return false, err
	}
	if err := t.storeInternal(right); err != nil {
		return false, err
	}
	if err := t.storeInternal(parent); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Tree) syncSeparator(parentID PageID, childIdx int) error {
	parent, err := t.loadInternal(parentID)
	if err != nil {
		return err
	}
	if childIdx <= 0 || childIdx >= len(parent.children) || len(parent.keys) == 0 {
		return nil
	}
	smallest, err := t.leftmostKey(parent.children[childIdx])
	if err != nil {
		return err
	}
	if bytesEqual(parent.keys[childIdx-1], smallest) {
		return nil
	}
	parent.keys[childIdx-1] = append([]byte(nil), smallest...)
	return t.storeInternal(parent)
}

func (t *Tree) collapseRoot(parent internalPage) error {
	if len(parent.keys) != 0 || len(parent.children) != 1 {
		return gerr.Newf(gerr.Corruption, "page %d cannot collapse", parent.id)
	}
	if err := t.setRoot(parent.children[0]); err != nil {
		return err
	}
	return t.freePage(parent.id)
}

func (t *Tree) pageUnderfull(id PageID) (bool, error) {
	typ, err := t.pageType(id)
	if err != nil {
		return false, err
	}
	if typ == TypeTreeLeaf {
		leaf, err := t.loadLeaf(id)
		if err != nil {
			return false, err
		}
		return leafUsed(leaf.cells) < t.payload/2, nil
	}
	if typ != TypeTreeInternal {
		return false, gerr.Newf(gerr.Corruption, "page %d has type %d", id, typ)
	}
	node, err := t.loadInternal(id)
	if err != nil {
		return false, err
	}
	return internalUsed(node.keys) < t.payload/2, nil
}

func deleteAt[T any](s []T, i int) []T {
	return append(s[:i], s[i+1:]...)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

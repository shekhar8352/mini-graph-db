package disk

import (
	"bytes"
	"sync"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

// Tree is one B+tree stored in a keyspace root slot.
//
// Methods are safe for concurrent use. Readers run together. A writer waits
// for readers, and readers wait for the writer. A cursor holds the read lock
// until Close, so do not call Get, Put, or Delete on that tree from the same
// goroutine while the cursor is open.
//
// Keys are ordered with bytes.Compare. An empty key is allowed. A nil value
// is stored as an empty value. Delete of a missing key is a success.
type Tree struct {
	mu       sync.RWMutex
	file     *PageFile
	pool     *Pool
	ks       storage.Keyspace
	payload  int
	pageSize int
}

// OpenTree binds a tree to one keyspace of file. The tree has no pages
// until the first Put. The pool needs a handful of frames for a walk and
// a split; tests use 32 or more.
func OpenTree(file *PageFile, pool *Pool, ks storage.Keyspace) (*Tree, error) {
	if file == nil || pool == nil {
		return nil, gerr.New(gerr.InvalidArgument, "tree file or pool is nil")
	}
	if _, err := rootSlot(ks); err != nil {
		return nil, err
	}
	return &Tree{
		file:     file,
		pool:     pool,
		ks:       ks,
		payload:  file.PayloadSize(),
		pageSize: file.PageSize(),
	}, nil
}

// Get returns a copy of the value for key, or storage.ErrNotFound.
func (t *Tree) Get(key []byte) ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	root, err := t.root()
	if err != nil {
		return nil, err
	}
	if root == 0 {
		return nil, storage.ErrNotFound
	}
	leaf, idx, _, err := t.descend(root, key)
	if err != nil {
		return nil, err
	}
	if idx >= len(leaf.cells) || !bytes.Equal(leaf.cells[idx].key, key) {
		return nil, storage.ErrNotFound
	}
	return t.cellValue(leaf.cells[idx])
}

// Put inserts or replaces key. The key must fit in an empty leaf with an
// overflow pointer. Values longer than a quarter of the page are stored on
// overflow pages.
func (t *Tree) Put(key, val []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkKey(key); err != nil {
		return err
	}
	if val == nil {
		val = []byte{}
	}
	root, err := t.root()
	if err != nil {
		return err
	}
	if root == 0 {
		return t.createRoot(key, val)
	}
	split, sep, right, err := t.insert(root, key, val)
	if err != nil || !split {
		return err
	}
	return t.growRoot(root, sep, right)
}

// Delete removes key. A missing key is not an error.
func (t *Tree) Delete(key []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	root, err := t.root()
	if err != nil || root == 0 {
		return err
	}
	_, err = t.remove(root, key, true)
	return err
}

func (t *Tree) createRoot(key, val []byte) error {
	cell, err := t.makeCell(key, val)
	if err != nil {
		return err
	}
	id, err := t.alloc(TypeTreeLeaf)
	if err != nil {
		t.freeOverflow(cell.overflow)
		return err
	}
	page := leafPage{id: id, cells: []leafCell{cell}}
	if err := t.storeLeaf(page); err != nil {
		t.freeOverflow(cell.overflow)
		_ = t.freePage(id)
		return err
	}
	if err := t.setRoot(id); err != nil {
		t.freeOverflow(cell.overflow)
		_ = t.freePage(id)
		return err
	}
	return nil
}

func (t *Tree) growRoot(left PageID, sep []byte, right PageID) error {
	id, err := t.alloc(TypeTreeInternal)
	if err != nil {
		return err
	}
	page := internalPage{
		id:       id,
		keys:     [][]byte{append([]byte(nil), sep...)},
		children: []PageID{left, right},
	}
	if err := t.storeInternal(page); err != nil {
		_ = t.freePage(id)
		return err
	}
	return t.setRoot(id)
}

func (t *Tree) insert(id PageID, key, val []byte) (split bool, sep []byte, right PageID, err error) {
	typ, err := t.pageType(id)
	if err != nil {
		return false, nil, 0, err
	}
	if typ == TypeTreeLeaf {
		return t.insertLeaf(id, key, val)
	}
	if typ != TypeTreeInternal {
		return false, nil, 0, gerr.Newf(gerr.Corruption, "page %d has type %d in an internal walk", id, typ)
	}
	node, err := t.loadInternal(id)
	if err != nil {
		return false, nil, 0, err
	}
	idx := childIndex(node.keys, key)
	split, sep, right, err = t.insert(node.children[idx], key, val)
	if err != nil || !split {
		return false, nil, 0, err
	}
	node, err = t.loadInternal(id)
	if err != nil {
		return false, nil, 0, err
	}
	node.keys = insertBytes(node.keys, idx, sep)
	node.children = insertPage(node.children, idx+1, right)
	if internalFits(t.payload, node.keys) {
		return false, nil, 0, t.storeInternal(node)
	}
	return t.splitInternal(node)
}

func (t *Tree) insertLeaf(id PageID, key, val []byte) (bool, []byte, PageID, error) {
	leaf, err := t.loadLeaf(id)
	if err != nil {
		return false, nil, 0, err
	}
	cell, err := t.makeCell(key, val)
	if err != nil {
		return false, nil, 0, err
	}
	idx, found := leafSearch(leaf.cells, key)
	var old PageID
	if found {
		old = leaf.cells[idx].overflow
		leaf.cells[idx] = cell
	} else {
		leaf.cells = insertCell(leaf.cells, idx, cell)
	}
	if leafFits(t.payload, leaf.cells) {
		if err := t.storeLeaf(leaf); err != nil {
			t.freeOverflow(cell.overflow)
			return false, nil, 0, err
		}
		if old != 0 && old != cell.overflow {
			t.freeOverflow(old)
		}
		return false, nil, 0, nil
	}
	split, sep, right, err := t.splitLeaf(leaf)
	if err != nil {
		t.freeOverflow(cell.overflow)
		return false, nil, 0, err
	}
	if old != 0 && old != cell.overflow {
		t.freeOverflow(old)
	}
	return split, sep, right, nil
}

func (t *Tree) splitLeaf(leaf leafPage) (bool, []byte, PageID, error) {
	leftCells, rightCells := splitLeafCells(leaf.cells)
	if len(leftCells) == 0 || len(rightCells) == 0 {
		return false, nil, 0, gerr.New(gerr.InvalidArgument, "key and value do not fit in a leaf")
	}
	rightID, err := t.alloc(TypeTreeLeaf)
	if err != nil {
		return false, nil, 0, err
	}
	right := leafPage{
		id:    rightID,
		left:  leaf.id,
		right: leaf.right,
		cells: rightCells,
	}
	leaf.cells = leftCells
	leaf.right = rightID
	if err := t.storeLeaf(leaf); err != nil {
		_ = t.freePage(rightID)
		return false, nil, 0, err
	}
	if err := t.storeLeaf(right); err != nil {
		_ = t.freePage(rightID)
		return false, nil, 0, err
	}
	if right.right != 0 {
		sib, err := t.loadLeaf(right.right)
		if err != nil {
			return false, nil, 0, err
		}
		sib.left = rightID
		if err := t.storeLeaf(sib); err != nil {
			return false, nil, 0, err
		}
	}
	return true, append([]byte(nil), rightCells[0].key...), rightID, nil
}

func (t *Tree) splitInternal(node internalPage) (bool, []byte, PageID, error) {
	if len(node.keys) < 2 {
		return false, nil, 0, gerr.New(gerr.InvalidArgument, "internal key does not fit")
	}
	mid := len(node.keys) / 2
	sep := append([]byte(nil), node.keys[mid]...)
	rightID, err := t.alloc(TypeTreeInternal)
	if err != nil {
		return false, nil, 0, err
	}
	right := internalPage{
		id:       rightID,
		keys:     append([][]byte(nil), node.keys[mid+1:]...),
		children: append([]PageID(nil), node.children[mid+1:]...),
	}
	node.keys = append([][]byte(nil), node.keys[:mid]...)
	node.children = append([]PageID(nil), node.children[:mid+1]...)
	if err := t.storeInternal(node); err != nil {
		_ = t.freePage(rightID)
		return false, nil, 0, err
	}
	if err := t.storeInternal(right); err != nil {
		_ = t.freePage(rightID)
		return false, nil, 0, err
	}
	return true, sep, rightID, nil
}

func (t *Tree) remove(id PageID, key []byte, isRoot bool) (bool, error) {
	typ, err := t.pageType(id)
	if err != nil {
		return false, err
	}
	if typ == TypeTreeLeaf {
		return t.removeLeaf(id, key, isRoot)
	}
	if typ != TypeTreeInternal {
		return false, gerr.Newf(gerr.Corruption, "page %d has type %d in a delete walk", id, typ)
	}
	node, err := t.loadInternal(id)
	if err != nil {
		return false, err
	}
	idx := childIndex(node.keys, key)
	found, err := t.remove(node.children[idx], key, false)
	if err != nil || !found {
		return found, err
	}
	return true, t.repair(id, idx, isRoot)
}

func (t *Tree) removeLeaf(id PageID, key []byte, isRoot bool) (bool, error) {
	leaf, err := t.loadLeaf(id)
	if err != nil {
		return false, err
	}
	idx, found := leafSearch(leaf.cells, key)
	if !found {
		return false, nil
	}
	old := leaf.cells[idx].overflow
	leaf.cells = append(leaf.cells[:idx], leaf.cells[idx+1:]...)
	if isRoot && len(leaf.cells) == 0 {
		if err := t.setRoot(0); err != nil {
			return true, err
		}
		if err := t.freePage(id); err != nil {
			return true, err
		}
		t.freeOverflow(old)
		return true, nil
	}
	if err := t.storeLeaf(leaf); err != nil {
		return true, err
	}
	t.freeOverflow(old)
	return true, nil
}

func (t *Tree) makeCell(key, val []byte) (leafCell, error) {
	cell := leafCell{key: append([]byte(nil), key...)}
	if len(val) > t.pageSize/4 {
		head, err := t.writeOverflow(val)
		if err != nil {
			return leafCell{}, err
		}
		cell.overflow = head
		cell.valLen = len(val)
	} else {
		cell.val = append([]byte(nil), val...)
		cell.valLen = len(val)
	}
	if leafCellSize(cell)+leafHeaderSize+2 > t.payload {
		t.freeOverflow(cell.overflow)
		return leafCell{}, gerr.New(gerr.InvalidArgument, "key and value do not fit in a leaf")
	}
	return cell, nil
}

func (t *Tree) checkKey(key []byte) error {
	if key == nil {
		key = []byte{}
	}
	maxKey := t.payload - leafHeaderSize - leafCellHeader - 8 - 2
	if len(key) > maxKey {
		return gerr.Newf(gerr.InvalidArgument, "key length %d exceeds %d", len(key), maxKey)
	}
	return nil
}

func (t *Tree) cellValue(c leafCell) ([]byte, error) {
	if c.overflow == 0 {
		return append([]byte(nil), c.val...), nil
	}
	return t.readOverflow(c.overflow, c.valLen)
}

func (t *Tree) descend(id PageID, key []byte) (leafPage, int, PageID, error) {
	for hop := 0; hop < maxTreeHeight; hop++ {
		typ, err := t.pageType(id)
		if err != nil {
			return leafPage{}, 0, 0, err
		}
		if typ == TypeTreeLeaf {
			leaf, err := t.loadLeaf(id)
			if err != nil {
				return leafPage{}, 0, 0, err
			}
			idx, _ := leafSearch(leaf.cells, key)
			return leaf, idx, id, nil
		}
		if typ != TypeTreeInternal {
			return leafPage{}, 0, 0, gerr.Newf(gerr.Corruption, "page %d has type %d in a search", id, typ)
		}
		node, err := t.loadInternal(id)
		if err != nil {
			return leafPage{}, 0, 0, err
		}
		if len(node.children) == 0 {
			return leafPage{}, 0, 0, gerr.Newf(gerr.Corruption, "internal page %d has no children", id)
		}
		id = node.children[childIndex(node.keys, key)]
	}
	return leafPage{}, 0, 0, gerr.New(gerr.Corruption, "tree is deeper than the height limit")
}

func (t *Tree) leftmostKey(id PageID) ([]byte, error) {
	for hop := 0; hop < maxTreeHeight; hop++ {
		typ, err := t.pageType(id)
		if err != nil {
			return nil, err
		}
		if typ == TypeTreeLeaf {
			leaf, err := t.loadLeaf(id)
			if err != nil {
				return nil, err
			}
			if len(leaf.cells) == 0 {
				return nil, gerr.Newf(gerr.Corruption, "leaf page %d has no keys", id)
			}
			return leaf.cells[0].key, nil
		}
		node, err := t.loadInternal(id)
		if err != nil {
			return nil, err
		}
		if len(node.children) == 0 {
			return nil, gerr.Newf(gerr.Corruption, "internal page %d has no children", id)
		}
		id = node.children[0]
	}
	return nil, gerr.New(gerr.Corruption, "tree is deeper than the height limit")
}

func (t *Tree) root() (PageID, error) {
	return t.file.Root(t.ks)
}

func (t *Tree) setRoot(id PageID) error {
	return t.file.SetRoot(t.ks, id)
}

func (t *Tree) pageType(id PageID) (PageType, error) {
	var typ PageType
	err := t.withPage(id, func(pg *Page) error {
		var e error
		typ, e = pg.Type()
		return e
	})
	return typ, err
}

func (t *Tree) withPage(id PageID, fn func(*Page) error) error {
	pg, err := t.pool.Get(id)
	if err != nil {
		return err
	}
	err = fn(pg)
	if uerr := pg.Unpin(); err == nil {
		err = uerr
	}
	return err
}

func (t *Tree) loadLeaf(id PageID) (leafPage, error) {
	var page leafPage
	err := t.withPage(id, func(pg *Page) error {
		typ, err := pg.Type()
		if err != nil {
			return err
		}
		if typ != TypeTreeLeaf {
			return gerr.Newf(gerr.Corruption, "page %d is type %d, want a leaf", id, typ)
		}
		data, err := pg.Data()
		if err != nil {
			return err
		}
		page, err = decodeLeaf(id, data)
		return err
	})
	return page, err
}

func (t *Tree) storeLeaf(page leafPage) error {
	return t.withPage(page.id, func(pg *Page) error {
		data, err := pg.Data()
		if err != nil {
			return err
		}
		buf := make([]byte, len(data))
		if err := encodeLeaf(buf, page); err != nil {
			return err
		}
		copy(data, buf)
		return pg.SetType(TypeTreeLeaf)
	})
}

func (t *Tree) loadInternal(id PageID) (internalPage, error) {
	var page internalPage
	err := t.withPage(id, func(pg *Page) error {
		typ, err := pg.Type()
		if err != nil {
			return err
		}
		if typ != TypeTreeInternal {
			return gerr.Newf(gerr.Corruption, "page %d is type %d, want an internal page", id, typ)
		}
		data, err := pg.Data()
		if err != nil {
			return err
		}
		page, err = decodeInternal(id, data)
		return err
	})
	return page, err
}

func (t *Tree) storeInternal(page internalPage) error {
	return t.withPage(page.id, func(pg *Page) error {
		data, err := pg.Data()
		if err != nil {
			return err
		}
		buf := make([]byte, len(data))
		if err := encodeInternal(buf, page); err != nil {
			return err
		}
		copy(data, buf)
		return pg.SetType(TypeTreeInternal)
	})
}

func (t *Tree) alloc(typ PageType) (PageID, error) {
	pg, err := t.pool.Alloc()
	if err != nil {
		return 0, err
	}
	id := pg.ID()
	if err := pg.SetType(typ); err != nil {
		_ = pg.Unpin()
		return 0, err
	}
	if err := pg.Unpin(); err != nil {
		return 0, err
	}
	return id, nil
}

func (t *Tree) freePage(id PageID) error {
	if id == 0 {
		return nil
	}
	pg, err := t.pool.Get(id)
	if err != nil {
		return err
	}
	return t.pool.Free(pg)
}

func (t *Tree) writeOverflow(val []byte) (PageID, error) {
	chunk := t.payload - overflowHeaderSize
	if chunk <= 0 {
		return 0, gerr.New(gerr.InvalidArgument, "page is too small for overflow")
	}
	n := (len(val) + chunk - 1) / chunk
	ids := make([]PageID, n)
	for i := range ids {
		id, err := t.alloc(TypeOverflow)
		if err != nil {
			for _, done := range ids[:i] {
				_ = t.freePage(done)
			}
			return 0, err
		}
		ids[i] = id
	}
	for i, id := range ids {
		start := i * chunk
		end := start + chunk
		if end > len(val) {
			end = len(val)
		}
		var next PageID
		if i+1 < len(ids) {
			next = ids[i+1]
		}
		if err := t.storeOverflow(id, next, val[start:end]); err != nil {
			for _, done := range ids {
				_ = t.freePage(done)
			}
			return 0, err
		}
	}
	return ids[0], nil
}

func (t *Tree) storeOverflow(id, next PageID, data []byte) error {
	return t.withPage(id, func(pg *Page) error {
		raw, err := pg.Data()
		if err != nil {
			return err
		}
		buf := make([]byte, len(raw))
		if err := encodeOverflow(buf, next, data); err != nil {
			return err
		}
		copy(raw, buf)
		return pg.SetType(TypeOverflow)
	})
}

func (t *Tree) readOverflow(head PageID, n int) ([]byte, error) {
	out := make([]byte, 0, n)
	id := head
	seen := 0
	for id != 0 {
		if seen > n && n >= 0 {
			return nil, gerr.Newf(gerr.Corruption, "overflow chain from %d is longer than %d bytes", head, n)
		}
		var next PageID
		var chunk []byte
		err := t.withPage(id, func(pg *Page) error {
			typ, err := pg.Type()
			if err != nil {
				return err
			}
			if typ != TypeOverflow {
				return gerr.Newf(gerr.Corruption, "page %d is type %d, want overflow", id, typ)
			}
			raw, err := pg.Data()
			if err != nil {
				return err
			}
			next, chunk, err = decodeOverflow(id, raw)
			return err
		})
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		seen += len(chunk)
		id = next
		if seen > maxTreeHeight*t.payload && n < 0 {
			return nil, gerr.New(gerr.Corruption, "overflow chain is cyclic or unbounded")
		}
	}
	if len(out) != n {
		return nil, gerr.Newf(gerr.Corruption, "overflow chain from %d has %d bytes, want %d", head, len(out), n)
	}
	return out, nil
}

func (t *Tree) freeOverflow(head PageID) {
	id := head
	seen := map[PageID]struct{}{}
	for id != 0 {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		var next PageID
		err := t.withPage(id, func(pg *Page) error {
			raw, err := pg.Data()
			if err != nil {
				return err
			}
			next, _, err = decodeOverflow(id, raw)
			return err
		})
		if err != nil {
			return
		}
		if err := t.freePage(id); err != nil {
			return
		}
		id = next
	}
}

func childIndex(keys [][]byte, key []byte) int {
	return binSearch(len(keys), func(i int) bool {
		return bytes.Compare(keys[i], key) > 0
	})
}

func leafSearch(cells []leafCell, key []byte) (int, bool) {
	i := binSearch(len(cells), func(i int) bool {
		return bytes.Compare(cells[i].key, key) >= 0
	})
	if i < len(cells) && bytes.Equal(cells[i].key, key) {
		return i, true
	}
	return i, false
}

func binSearch(n int, pred func(int) bool) int {
	lo, hi := 0, n
	for lo < hi {
		mid := lo + (hi-lo)/2
		if pred(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

func splitLeafCells(cells []leafCell) (left, right []leafCell) {
	if len(cells) < 2 {
		return cells, nil
	}
	total := 0
	for _, c := range cells {
		total += leafCellSize(c) + 2
	}
	half := total / 2
	used := 0
	i := 0
	for i < len(cells)-1 {
		next := leafCellSize(cells[i]) + 2
		if i > 0 && used+next > half {
			break
		}
		used += next
		i++
		if used >= half {
			break
		}
	}
	if i == 0 {
		i = 1
	}
	if i >= len(cells) {
		i = len(cells) - 1
	}
	return cells[:i], cells[i:]
}

func insertCell(cells []leafCell, idx int, cell leafCell) []leafCell {
	cells = append(cells, leafCell{})
	copy(cells[idx+1:], cells[idx:])
	cells[idx] = cell
	return cells
}

func insertBytes(keys [][]byte, idx int, key []byte) [][]byte {
	keys = append(keys, nil)
	copy(keys[idx+1:], keys[idx:])
	keys[idx] = key
	return keys
}

func insertPage(children []PageID, idx int, id PageID) []PageID {
	children = append(children, 0)
	copy(children[idx+1:], children[idx:])
	children[idx] = id
	return children
}

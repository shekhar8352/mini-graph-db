package disk

import (
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Leaf and internal pages are slotted. Cell bytes grow up from a fixed
// header. A directory of uint16 offsets grows down from the end of the
// payload. Keys are stored in full; there is no prefix compression.
//
// A value longer than a quarter of the page is not stored in the leaf.
// The cell holds the value length and the id of the first overflow page.

const (
	leafHeaderSize     = 20
	internalHeaderSize = 12
	leafCellHeader     = 8
	internalCellHeader = 12
	overflowHeaderSize = 12
	cellFlagOverflow   = 1
	maxTreeHeight      = 64
)

type leafCell struct {
	key      []byte
	val      []byte
	valLen   int
	overflow PageID
}

type leafPage struct {
	id    PageID
	left  PageID
	right PageID
	cells []leafCell
}

type internalPage struct {
	id       PageID
	keys     [][]byte
	children []PageID
}

func leafCellSize(c leafCell) int {
	n := leafCellHeader + len(c.key)
	if c.overflow != 0 {
		return n + 8
	}
	return n + len(c.val)
}

func leafUsed(cells []leafCell) int {
	n := leafHeaderSize
	for _, c := range cells {
		n += leafCellSize(c) + 2
	}
	return n
}

func leafFits(payload int, cells []leafCell) bool {
	return leafUsed(cells) <= payload
}

func internalCellSize(key []byte) int {
	return internalCellHeader + len(key)
}

func internalUsed(keys [][]byte) int {
	n := internalHeaderSize
	for _, key := range keys {
		n += internalCellSize(key) + 2
	}
	return n
}

func internalFits(payload int, keys [][]byte) bool {
	return internalUsed(keys) <= payload
}

func encodeLeaf(dst []byte, p leafPage) error {
	if !leafFits(len(dst), p.cells) {
		return gerr.New(gerr.InvalidArgument, "leaf page does not fit")
	}
	clear(dst)
	binary.LittleEndian.PutUint16(dst[0:2], uint16(len(p.cells)))
	binary.LittleEndian.PutUint64(dst[4:12], uint64(p.left))
	binary.LittleEndian.PutUint64(dst[12:20], uint64(p.right))
	off := leafHeaderSize
	for i, c := range p.cells {
		if len(c.key) > 0xffff {
			return gerr.New(gerr.InvalidArgument, "key is too long")
		}
		binary.LittleEndian.PutUint16(dst[off:off+2], uint16(len(c.key)))
		valLen := c.valLen
		if c.overflow == 0 {
			valLen = len(c.val)
		}
		if valLen < 0 || valLen > 0xffffffff {
			return gerr.New(gerr.InvalidArgument, "value is too long")
		}
		binary.LittleEndian.PutUint32(dst[off+4:off+8], uint32(valLen))
		copy(dst[off+8:], c.key)
		next := off + 8 + len(c.key)
		if c.overflow != 0 {
			dst[off+2] = cellFlagOverflow
			binary.LittleEndian.PutUint64(dst[next:next+8], uint64(c.overflow))
			next += 8
		} else {
			copy(dst[next:], c.val)
			next += len(c.val)
		}
		slot := len(dst) - 2*(i+1)
		binary.LittleEndian.PutUint16(dst[slot:slot+2], uint16(off))
		off = next
	}
	return nil
}

func decodeLeaf(id PageID, src []byte) (leafPage, error) {
	p := leafPage{id: id}
	if len(src) < leafHeaderSize {
		return p, gerr.Newf(gerr.Corruption, "leaf page %d is truncated", id)
	}
	n := int(binary.LittleEndian.Uint16(src[0:2]))
	if leafHeaderSize+2*n > len(src) {
		return p, gerr.Newf(gerr.Corruption, "leaf page %d has %d cells", id, n)
	}
	p.left = PageID(binary.LittleEndian.Uint64(src[4:12]))
	p.right = PageID(binary.LittleEndian.Uint64(src[12:20]))
	p.cells = make([]leafCell, n)
	for i := 0; i < n; i++ {
		off := int(binary.LittleEndian.Uint16(src[len(src)-2*(i+1):]))
		cell, err := decodeLeafCell(id, src, off)
		if err != nil {
			return p, err
		}
		p.cells[i] = cell
	}
	return p, nil
}

func decodeLeafCell(id PageID, src []byte, off int) (leafCell, error) {
	var c leafCell
	if off < leafHeaderSize || off+leafCellHeader > len(src) {
		return c, gerr.Newf(gerr.Corruption, "leaf page %d has a cell offset %d", id, off)
	}
	keyLen := int(binary.LittleEndian.Uint16(src[off : off+2]))
	flag := src[off+2]
	valLen := int(binary.LittleEndian.Uint32(src[off+4 : off+8]))
	body := off + leafCellHeader
	if keyLen < 0 || body+keyLen > len(src) {
		return c, gerr.Newf(gerr.Corruption, "leaf page %d has a bad key length", id)
	}
	c.key = append([]byte(nil), src[body:body+keyLen]...)
	body += keyLen
	c.valLen = valLen
	if flag == cellFlagOverflow {
		if body+8 > len(src) {
			return c, gerr.Newf(gerr.Corruption, "leaf page %d has a truncated overflow pointer", id)
		}
		c.overflow = PageID(binary.LittleEndian.Uint64(src[body : body+8]))
		if c.overflow == 0 {
			return c, gerr.Newf(gerr.Corruption, "leaf page %d has an empty overflow pointer", id)
		}
		return c, nil
	}
	if flag != 0 {
		return c, gerr.Newf(gerr.Corruption, "leaf page %d has cell flag %d", id, flag)
	}
	if valLen < 0 || body+valLen > len(src) {
		return c, gerr.Newf(gerr.Corruption, "leaf page %d has a bad value length", id)
	}
	c.val = append([]byte(nil), src[body:body+valLen]...)
	return c, nil
}

func encodeInternal(dst []byte, p internalPage) error {
	if len(p.children) != len(p.keys)+1 {
		return gerr.Newf(gerr.Corruption, "internal page %d has %d keys and %d children", p.id, len(p.keys), len(p.children))
	}
	if !internalFits(len(dst), p.keys) {
		return gerr.New(gerr.InvalidArgument, "internal page does not fit")
	}
	clear(dst)
	binary.LittleEndian.PutUint16(dst[0:2], uint16(len(p.keys)))
	rightmost := PageID(0)
	if len(p.children) > 0 {
		rightmost = p.children[len(p.children)-1]
	}
	binary.LittleEndian.PutUint64(dst[4:12], uint64(rightmost))
	off := internalHeaderSize
	for i, key := range p.keys {
		if len(key) > 0xffff {
			return gerr.New(gerr.InvalidArgument, "key is too long")
		}
		binary.LittleEndian.PutUint16(dst[off:off+2], uint16(len(key)))
		binary.LittleEndian.PutUint64(dst[off+4:off+12], uint64(p.children[i]))
		copy(dst[off+internalCellHeader:], key)
		slot := len(dst) - 2*(i+1)
		binary.LittleEndian.PutUint16(dst[slot:slot+2], uint16(off))
		off += internalCellHeader + len(key)
	}
	return nil
}

func decodeInternal(id PageID, src []byte) (internalPage, error) {
	p := internalPage{id: id}
	if len(src) < internalHeaderSize {
		return p, gerr.Newf(gerr.Corruption, "internal page %d is truncated", id)
	}
	n := int(binary.LittleEndian.Uint16(src[0:2]))
	if internalHeaderSize+2*n > len(src) {
		return p, gerr.Newf(gerr.Corruption, "internal page %d has %d keys", id, n)
	}
	rightmost := PageID(binary.LittleEndian.Uint64(src[4:12]))
	p.keys = make([][]byte, n)
	p.children = make([]PageID, n+1)
	for i := 0; i < n; i++ {
		off := int(binary.LittleEndian.Uint16(src[len(src)-2*(i+1):]))
		if off < internalHeaderSize || off+internalCellHeader > len(src) {
			return p, gerr.Newf(gerr.Corruption, "internal page %d has a cell offset %d", id, off)
		}
		keyLen := int(binary.LittleEndian.Uint16(src[off : off+2]))
		if keyLen < 0 || off+internalCellHeader+keyLen > len(src) {
			return p, gerr.Newf(gerr.Corruption, "internal page %d has a bad key length", id)
		}
		p.children[i] = PageID(binary.LittleEndian.Uint64(src[off+4 : off+12]))
		if p.children[i] == 0 {
			return p, gerr.Newf(gerr.Corruption, "internal page %d has an empty child", id)
		}
		p.keys[i] = append([]byte(nil), src[off+internalCellHeader:off+internalCellHeader+keyLen]...)
	}
	if n == 0 && rightmost == 0 {
		p.children = nil
		return p, nil
	}
	if rightmost == 0 {
		return p, gerr.Newf(gerr.Corruption, "internal page %d has an empty right child", id)
	}
	p.children[n] = rightmost
	return p, nil
}

func encodeOverflow(dst []byte, next PageID, data []byte) error {
	if overflowHeaderSize+len(data) > len(dst) {
		return gerr.New(gerr.InvalidArgument, "overflow page does not fit")
	}
	clear(dst)
	binary.LittleEndian.PutUint64(dst[0:8], uint64(next))
	binary.LittleEndian.PutUint32(dst[8:12], uint32(len(data)))
	copy(dst[overflowHeaderSize:], data)
	return nil
}

func decodeOverflow(id PageID, src []byte) (PageID, []byte, error) {
	if len(src) < overflowHeaderSize {
		return 0, nil, gerr.Newf(gerr.Corruption, "overflow page %d is truncated", id)
	}
	next := PageID(binary.LittleEndian.Uint64(src[0:8]))
	n := int(binary.LittleEndian.Uint32(src[8:12]))
	if n < 0 || overflowHeaderSize+n > len(src) {
		return 0, nil, gerr.Newf(gerr.Corruption, "overflow page %d has length %d", id, n)
	}
	return next, append([]byte(nil), src[overflowHeaderSize:overflowHeaderSize+n]...), nil
}

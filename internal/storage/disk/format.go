// Package disk is the page file and buffer pool for the on-disk engine.
// The B+tree and the write-ahead log are later Phase 2 work; they sit on
// the pages defined here.
package disk

import (
	"encoding/binary"
	"hash/crc32"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

const (
	// Magic is the four bytes at the start of the file-header payload.
	Magic = "GRDB"
	// FormatVersion is the page-file version this build reads and writes.
	// A newer version is refused. Version 1 is the first page format.
	FormatVersion uint16 = 1
	// DefaultPageSize is the page size used when Create is given zero.
	DefaultPageSize = 8192
	// MinPageSize is the smallest page size Create accepts.
	MinPageSize = 4096
	// MaxPageSize is the largest page size Create accepts.
	MaxPageSize = 65536
)

// PageID is an index into the page file. Page 0 is the file header.
// Zero is also the freelist terminator, because the header is never free.
type PageID uint64

// PageType is the kind stored in the page header.
type PageType uint16

const (
	// TypeHeader is page 0.
	TypeHeader PageType = 1
	// TypeFree is a page on the freelist.
	TypeFree PageType = 2
	// TypeData is an opaque payload page. Phase 2C uses the reserved types
	// below for tree and overflow pages; this phase only writes header, free,
	// and data.
	TypeData PageType = 3
	// TypeTreeLeaf is reserved for the B+tree leaf layout.
	TypeTreeLeaf PageType = 4
	// TypeTreeInternal is reserved for the B+tree internal layout.
	TypeTreeInternal PageType = 5
	// TypeOverflow is reserved for values that do not fit in a leaf cell.
	TypeOverflow PageType = 6
)

const (
	pageHeaderSize  = 24
	pageTrailerSize = 4
	pageOverhead    = pageHeaderSize + pageTrailerSize

	offMagic      = 0
	offVersion    = 4
	offPageSize   = 8
	offUUID       = 12
	offCreated    = 28
	offCheckpoint = 36
	offFreelist   = 44
	offPageCount  = 52
	offFreeCount  = 60
	offRoots      = 68
	rootSlots     = 9
	metaBytes     = offRoots + rootSlots*8
)

// rootOrder is the on-disk order of tree-root slots in the file header.
var rootOrder = []storage.Keyspace{
	storage.KSNode,
	storage.KSEdge,
	storage.KSOut,
	storage.KSIn,
	storage.KSLabel,
	storage.KSEdgeType,
	storage.KSProp,
	storage.KSCatalog,
	storage.KSVersion,
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// fileMeta is the parsed file header. PageCount includes page 0.
type fileMeta struct {
	version    uint16
	pageSize   int
	uuid       [16]byte
	created    int64
	checkpoint uint64
	freelist   PageID
	pageCount  uint64
	freeCount  uint64
	roots      [rootSlots]PageID
}

func checkPageSize(n int) error {
	if n < MinPageSize || n > MaxPageSize || n&(n-1) != 0 {
		return gerr.Newf(gerr.InvalidArgument, "page size %d is not a power of two between %d and %d", n, MinPageSize, MaxPageSize)
	}
	if n-pageOverhead < metaBytes {
		return gerr.Newf(gerr.InvalidArgument, "page size %d is too small for the file header", n)
	}
	return nil
}

func seal(page []byte) {
	sum := crc32.Checksum(page[:len(page)-pageTrailerSize], crcTable)
	binary.LittleEndian.PutUint32(page[len(page)-pageTrailerSize:], sum)
}

func verifyChecksum(page []byte) error {
	want := crc32.Checksum(page[:len(page)-pageTrailerSize], crcTable)
	got := binary.LittleEndian.Uint32(page[len(page)-pageTrailerSize:])
	if want != got {
		return gerr.Newf(gerr.Corruption, "page %d checksum mismatch", pageIDOf(page))
	}
	return nil
}

func pageIDOf(page []byte) PageID {
	return PageID(binary.LittleEndian.Uint64(page[0:8]))
}

func setPageID(page []byte, id PageID) {
	binary.LittleEndian.PutUint64(page[0:8], uint64(id))
}

func pageLSNOf(page []byte) uint64 {
	return binary.LittleEndian.Uint64(page[8:16])
}

func setPageLSN(page []byte, lsn uint64) {
	binary.LittleEndian.PutUint64(page[8:16], lsn)
}

func pageTypeOf(page []byte) PageType {
	return PageType(binary.LittleEndian.Uint16(page[16:18]))
}

func setPageType(page []byte, typ PageType) {
	binary.LittleEndian.PutUint16(page[16:18], uint16(typ))
}

func payload(page []byte) []byte {
	return page[pageHeaderSize : len(page)-pageTrailerSize]
}

func freeNext(page []byte) PageID {
	return PageID(binary.LittleEndian.Uint64(payload(page)[:8]))
}

func setFreeNext(page []byte, next PageID) {
	binary.LittleEndian.PutUint64(payload(page)[:8], uint64(next))
}

func blankPage(pageSize int, id PageID, typ PageType, lsn uint64) []byte {
	page := make([]byte, pageSize)
	setPageID(page, id)
	setPageLSN(page, lsn)
	setPageType(page, typ)
	seal(page)
	return page
}

func encodeMeta(dst []byte, m fileMeta) {
	copy(dst[offMagic:offMagic+4], Magic)
	binary.LittleEndian.PutUint16(dst[offVersion:], m.version)
	binary.LittleEndian.PutUint32(dst[offPageSize:], uint32(m.pageSize))
	copy(dst[offUUID:offUUID+16], m.uuid[:])
	binary.LittleEndian.PutUint64(dst[offCreated:], uint64(m.created))
	binary.LittleEndian.PutUint64(dst[offCheckpoint:], m.checkpoint)
	binary.LittleEndian.PutUint64(dst[offFreelist:], uint64(m.freelist))
	binary.LittleEndian.PutUint64(dst[offPageCount:], m.pageCount)
	binary.LittleEndian.PutUint64(dst[offFreeCount:], m.freeCount)
	for i, id := range m.roots {
		binary.LittleEndian.PutUint64(dst[offRoots+i*8:], uint64(id))
	}
}

func decodeMeta(src []byte) (fileMeta, error) {
	var m fileMeta
	if len(src) < metaBytes {
		return m, gerr.New(gerr.Corruption, "file header is truncated")
	}
	if string(src[offMagic:offMagic+4]) != Magic {
		return m, gerr.New(gerr.InvalidArgument, "file is not a graphdb page file")
	}
	m.version = binary.LittleEndian.Uint16(src[offVersion:])
	m.pageSize = int(binary.LittleEndian.Uint32(src[offPageSize:]))
	copy(m.uuid[:], src[offUUID:offUUID+16])
	m.created = int64(binary.LittleEndian.Uint64(src[offCreated:]))
	m.checkpoint = binary.LittleEndian.Uint64(src[offCheckpoint:])
	m.freelist = PageID(binary.LittleEndian.Uint64(src[offFreelist:]))
	m.pageCount = binary.LittleEndian.Uint64(src[offPageCount:])
	m.freeCount = binary.LittleEndian.Uint64(src[offFreeCount:])
	for i := range m.roots {
		m.roots[i] = PageID(binary.LittleEndian.Uint64(src[offRoots+i*8:]))
	}
	return m, nil
}

func rootSlot(ks storage.Keyspace) (int, error) {
	for i, candidate := range rootOrder {
		if candidate == ks {
			return i, nil
		}
	}
	return 0, gerr.Newf(gerr.InvalidArgument, "unknown keyspace %q", string(ks))
}

func corruptionf(format string, args ...any) error {
	return gerr.Newf(gerr.Corruption, format, args...)
}

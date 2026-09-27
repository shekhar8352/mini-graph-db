package wal

import (
	"encoding/binary"
	"hash/crc32"
	"io"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Magic is the four bytes at the start of every segment.
const Magic = "GRWL"

// FormatVersion is the WAL version this build reads and writes.
// A newer version is refused. Version 1 is the first binary log.
const FormatVersion uint16 = 1

// MaxPayload is the largest record payload Append accepts.
const MaxPayload = 16 << 20

// DefaultSegmentSize is the segment file size used when Options leaves it unset.
// A single record may run past this when it is the first record in the segment.
const DefaultSegmentSize int64 = 64 << 20

// LSN is the position of a record in the logical log. The first record is 1.
// Zero means no record: a page that has never been logged, and a checkpoint
// that has not been written. The value is 1 plus the count of record bytes
// before this record. Segment headers are not part of that count.
type LSN uint64

// Type is the kind of redo record stored in a frame.
type Type uint16

const (
	// TypePageWrite carries a full page image.
	TypePageWrite Type = 1
	// TypeTreeInsert carries one key and value for a keyspace.
	TypeTreeInsert Type = 2
	// TypeTreeDelete carries one key for a keyspace.
	TypeTreeDelete Type = 3
	// TypeTxnBegin marks the start of a transaction. The payload is empty in this version.
	TypeTxnBegin Type = 4
	// TypeTxnCommit marks a committed transaction. The payload may be empty.
	TypeTxnCommit Type = 5
	// TypeTxnAbort marks a rolled-back transaction. The payload is empty in this version.
	TypeTxnAbort Type = 6
	// TypeCheckpoint marks a durable point the checkpointer will use in Phase 2E.
	TypeCheckpoint Type = 7
)

const (
	headerSize   = 28
	recordHeader = 24
	crcSize      = 4
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one framed WAL entry. Payload is owned by the caller.
type Record struct {
	LSN     LSN
	TxnID   uint64
	Type    Type
	Payload []byte
}

// Size is the number of bytes the record occupies in a segment, including the checksum.
func (r Record) Size() int {
	return recordHeader + len(r.Payload) + crcSize
}

func validType(t Type) bool {
	return t >= TypePageWrite && t <= TypeCheckpoint
}

func appendRecord(dst []byte, lsn LSN, txn uint64, typ Type, payload []byte) []byte {
	i := len(dst)
	dst = append(dst, make([]byte, recordHeader+len(payload)+crcSize)...)
	binary.LittleEndian.PutUint64(dst[i:], uint64(lsn))
	binary.LittleEndian.PutUint64(dst[i+8:], txn)
	binary.LittleEndian.PutUint16(dst[i+16:], uint16(typ))
	binary.LittleEndian.PutUint32(dst[i+20:], uint32(len(payload)))
	copy(dst[i+recordHeader:], payload)
	sum := crc32.Checksum(dst[i:i+recordHeader+len(payload)], crcTable)
	binary.LittleEndian.PutUint32(dst[i+recordHeader+len(payload):], sum)
	return dst
}

func encodeHeader(num uint64, first LSN) []byte {
	b := make([]byte, headerSize)
	copy(b[:4], Magic)
	binary.LittleEndian.PutUint16(b[4:6], FormatVersion)
	binary.LittleEndian.PutUint64(b[8:16], num)
	binary.LittleEndian.PutUint64(b[16:24], uint64(first))
	sum := crc32.Checksum(b[:24], crcTable)
	binary.LittleEndian.PutUint32(b[24:28], sum)
	return b
}

// EncodePageWrite builds the payload of a TypePageWrite record.
func EncodePageWrite(pageID uint64, image []byte) ([]byte, error) {
	if len(image) == 0 {
		return nil, gerr.New(gerr.InvalidArgument, "page image is empty")
	}
	buf := make([]byte, 8+len(image))
	binary.LittleEndian.PutUint64(buf[:8], pageID)
	copy(buf[8:], image)
	return buf, nil
}

// DecodePageWrite reads a TypePageWrite payload.
func DecodePageWrite(payload []byte) (pageID uint64, image []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, gerr.New(gerr.Corruption, "page write payload is truncated")
	}
	pageID = binary.LittleEndian.Uint64(payload[:8])
	image = append([]byte(nil), payload[8:]...)
	if len(image) == 0 {
		return 0, nil, gerr.New(gerr.Corruption, "page write image is empty")
	}
	return pageID, image, nil
}

// EncodeTreeInsert builds the payload of a TypeTreeInsert record.
// A nil key or value is stored as empty.
func EncodeTreeInsert(keyspace byte, key, val []byte) []byte {
	buf := make([]byte, 1+4+len(key)+4+len(val))
	buf[0] = keyspace
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(key)))
	copy(buf[5:], key)
	off := 5 + len(key)
	binary.LittleEndian.PutUint32(buf[off:off+4], uint32(len(val)))
	copy(buf[off+4:], val)
	return buf
}

// DecodeTreeInsert reads a TypeTreeInsert payload.
func DecodeTreeInsert(payload []byte) (keyspace byte, key, val []byte, err error) {
	if len(payload) < 1+4 {
		return 0, nil, nil, gerr.New(gerr.Corruption, "tree insert payload is truncated")
	}
	keyspace = payload[0]
	keyLen := int(binary.LittleEndian.Uint32(payload[1:5]))
	if keyLen < 0 || 5+keyLen+4 > len(payload) {
		return 0, nil, nil, gerr.New(gerr.Corruption, "tree insert key is truncated")
	}
	key = append([]byte(nil), payload[5:5+keyLen]...)
	off := 5 + keyLen
	valLen := int(binary.LittleEndian.Uint32(payload[off : off+4]))
	off += 4
	if valLen < 0 || off+valLen != len(payload) {
		return 0, nil, nil, gerr.New(gerr.Corruption, "tree insert value is truncated")
	}
	val = append([]byte(nil), payload[off:off+valLen]...)
	return keyspace, key, val, nil
}

// EncodeTreeDelete builds the payload of a TypeTreeDelete record.
// A nil key is stored as empty.
func EncodeTreeDelete(keyspace byte, key []byte) []byte {
	buf := make([]byte, 1+4+len(key))
	buf[0] = keyspace
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(key)))
	copy(buf[5:], key)
	return buf
}

// DecodeTreeDelete reads a TypeTreeDelete payload.
func DecodeTreeDelete(payload []byte) (keyspace byte, key []byte, err error) {
	if len(payload) < 5 {
		return 0, nil, gerr.New(gerr.Corruption, "tree delete payload is truncated")
	}
	keyspace = payload[0]
	keyLen := int(binary.LittleEndian.Uint32(payload[1:5]))
	if keyLen < 0 || 5+keyLen != len(payload) {
		return 0, nil, gerr.New(gerr.Corruption, "tree delete key is truncated")
	}
	key = append([]byte(nil), payload[5:]...)
	return keyspace, key, nil
}

// EncodeCheckpoint builds the payload of a TypeCheckpoint record.
// unixNano is the wall time of the checkpoint.
func EncodeCheckpoint(unixNano int64) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(unixNano))
	return buf
}

// DecodeCheckpoint reads a TypeCheckpoint payload.
func DecodeCheckpoint(payload []byte) (unixNano int64, err error) {
	if len(payload) != 8 {
		return 0, gerr.New(gerr.Corruption, "checkpoint payload has the wrong length")
	}
	return int64(binary.LittleEndian.Uint64(payload)), nil
}

func readFullAt(r io.ReaderAt, p []byte, off int64) error {
	n := 0
	for n < len(p) {
		nn, err := r.ReadAt(p[n:], off+int64(n))
		n += nn
		if n == len(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if nn == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Reader walks synced records in LSN order.
// Next returns io.EOF after the last record. A partial trailing record is
// not returned. Close releases the segment files; it does not close the log.
type Reader struct {
	segs   []rseg
	si     int
	off    int64
	held   *Record
	err    error
	closed bool
}

type rseg struct {
	f     *os.File
	first LSN
	end   LSN
	pos   LSN
	limit int64
}

// Reader returns a reader positioned at the first synced record.
func (l *Log) Reader() (*Reader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	spans := make([]rseg, 0, len(l.sealed)+1)
	for _, info := range l.sealed {
		seg, err := openSpan(info.Path, info.First, info.End, 0)
		if err != nil {
			closeSpans(spans)
			return nil, err
		}
		spans = append(spans, seg)
	}
	if info, limit, ok := l.currentSpan(); ok {
		seg, err := openSpan(info.Path, info.First, info.End, limit)
		if err != nil {
			closeSpans(spans)
			return nil, err
		}
		spans = append(spans, seg)
	}
	r := &Reader{segs: spans}
	r.reset()
	return r, nil
}

func (l *Log) currentSpan() (SegmentInfo, int64, bool) {
	if l.path == "" || l.syncedOff <= headerSize {
		return SegmentInfo{}, 0, false
	}
	for _, s := range l.sealed {
		if s.Number == l.segNum {
			return SegmentInfo{}, 0, false
		}
	}
	end := l.segFirst + LSN(l.syncedOff-int64(headerSize))
	info := SegmentInfo{Number: l.segNum, Path: l.path, First: l.segFirst, End: end}
	return info, l.syncedOff, true
}

func openSpan(path string, first, end LSN, limit int64) (rseg, error) {
	f, err := os.Open(path)
	if err != nil {
		return rseg{}, gerr.Wrap(gerr.Unavailable, "open wal segment", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return rseg{}, gerr.Wrap(gerr.Unavailable, "stat wal segment", err)
	}
	if limit == 0 || limit > info.Size() {
		limit = info.Size()
	}
	if limit < headerSize {
		_ = f.Close()
		return rseg{}, gerr.Newf(gerr.Corruption, "wal segment %s has a short header", path)
	}
	return rseg{f: f, first: first, end: end, pos: first, limit: limit}, nil
}

func closeSpans(segs []rseg) {
	for i := range segs {
		_ = segs[i].f.Close()
	}
}

func (r *Reader) reset() {
	r.si = 0
	r.off = headerSize
	r.held = nil
	r.err = nil
	for i := range r.segs {
		r.segs[i].pos = r.segs[i].first
	}
}

// Seek positions the reader on the first record with LSN >= lsn.
// An lsn of 0 selects the first record. Seeking past the end is not an error;
// the following Next returns io.EOF.
func (r *Reader) Seek(lsn LSN) error {
	if r.closed {
		return ErrClosed
	}
	if lsn < 1 {
		lsn = 1
	}
	r.reset()
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			r.err = nil
			return nil
		}
		if err != nil {
			return err
		}
		if rec.LSN >= lsn {
			cp := rec
			r.held = &cp
			return nil
		}
	}
}

// Next returns the next record. io.EOF ends the log.
func (r *Reader) Next() (Record, error) {
	if r.closed {
		return Record{}, ErrClosed
	}
	if r.err != nil {
		return Record{}, r.err
	}
	if r.held != nil {
		rec := *r.held
		r.held = nil
		return rec, nil
	}
	for r.si < len(r.segs) {
		seg := &r.segs[r.si]
		if r.off >= seg.limit {
			if seg.pos != seg.end {
				err := gerr.Newf(gerr.Corruption, "wal segment ended at lsn %d, want %d", seg.pos, seg.end)
				r.err = err
				return Record{}, err
			}
			r.si++
			r.off = headerSize
			continue
		}
		rec, next, err := readRecord(seg.f, r.off, seg.limit, seg.pos)
		if err != nil {
			r.err = err
			return Record{}, err
		}
		r.off = next
		seg.pos = rec.LSN + LSN(rec.Size())
		return rec, nil
	}
	return Record{}, io.EOF
}

// Close releases segment files opened for this reader.
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	var err error
	for i := range r.segs {
		if cerr := r.segs[i].f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func readRecord(f *os.File, off, limit int64, expect LSN) (Record, int64, error) {
	remain := limit - off
	if remain < recordHeader {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d is truncated", expect)
	}
	var hdr [recordHeader]byte
	if err := readFullAt(f, hdr[:], off); err != nil {
		return Record{}, 0, gerr.Wrap(gerr.Unavailable, "read wal record", err)
	}
	payLen := binary.LittleEndian.Uint32(hdr[20:24])
	if payLen > MaxPayload {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d has length %d", expect, payLen)
	}
	total := int64(recordHeader) + int64(payLen) + crcSize
	if off+total > limit {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d is truncated", expect)
	}
	buf := make([]byte, total)
	if err := readFullAt(f, buf, off); err != nil {
		return Record{}, 0, gerr.Wrap(gerr.Unavailable, "read wal record", err)
	}
	sum := crc32.Checksum(buf[:len(buf)-crcSize], crcTable)
	got := binary.LittleEndian.Uint32(buf[len(buf)-crcSize:])
	if sum != got {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d checksum mismatch", expect)
	}
	rec := Record{
		LSN:     LSN(binary.LittleEndian.Uint64(buf[0:8])),
		TxnID:   binary.LittleEndian.Uint64(buf[8:16]),
		Type:    Type(binary.LittleEndian.Uint16(buf[16:18])),
		Payload: append([]byte(nil), buf[recordHeader:recordHeader+payLen]...),
	}
	if rec.LSN != expect {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record lsn %d, want %d", rec.LSN, expect)
	}
	if !validType(rec.Type) {
		return Record{}, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d has type %d", expect, rec.Type)
	}
	return rec, off + total, nil
}

// Package wal is a segmented binary write-ahead log.
// A record is durable after Sync returns nil. Applying records to pages is Phase 2E.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// ErrClosed is returned by Append, Sync, and Reader after Close.
var ErrClosed = errors.New("wal: closed")

// SegmentInfo describes one sealed segment file.
// End is the LSN of the next record, so the segment covers [First, End).
type SegmentInfo struct {
	Number uint64
	Path   string
	First  LSN
	End    LSN
}

// Archiver is notified after a segment is sealed and synced.
// Archive must be idempotent: a crash can ask for the same segment again.
// The hook runs while the log is locked and must not call back into the log.
// Phase 8 and Phase 10 supply the copy. A nil archiver skips the hook.
type Archiver interface {
	Archive(SegmentInfo) error
}

// Options configures a log. Zero values select the defaults.
type Options struct {
	// SegmentSize is the soft cap on a segment file, including its header.
	// Zero selects DefaultSegmentSize. A record that is the first in a
	// segment may make that file longer than SegmentSize.
	SegmentSize int64
	// GroupCommit is how long a Sync waits when another Sync is already
	// queued, so one fsync can cover the batch. Zero selects 1ms.
	// A Sync that is alone does not wait.
	GroupCommit time.Duration
	// Archiver receives each sealed segment. Nil skips archiving.
	Archiver Archiver
}

// Log is one segmented write-ahead log. Append buffers records.
// Sync makes every successful Append durable. Close syncs and then closes.
//
// Append and Sync are safe for concurrent use. A cursor-style Reader sees
// records that have been synced. The log does not apply records to pages;
// that is the disk engine in Phase 2E.
type Log struct {
	mu       sync.Mutex
	cond     *sync.Cond
	dir      string
	segSize  int64
	group    time.Duration
	archiver Archiver

	seg       *os.File
	path      string
	segNum    uint64
	segFirst  LSN
	segLen    int64
	syncedOff int64
	sealed    []SegmentInfo
	pending   *SegmentInfo

	next    LSN
	fileEnd LSN
	durable LSN
	buf     []byte

	flushing bool
	waiters  []waiter
	closed   bool
	broken   error

	// Test hooks. Nil in production.
	delayFn   func(waiters int) time.Duration
	writeHook func(f *os.File, p []byte) (int, error)
	syncHook  func(f *os.File) error
}

type waiter struct {
	end  LSN
	errc chan error
}

// Open creates dir if needed and opens the log stored there.
// Segment files are named 000000000001.wal and up. A torn tail on the last
// segment is truncated. A bad checksum on a complete record is corruption.
func Open(dir string, opt Options) (*Log, error) {
	if dir == "" {
		return nil, gerr.New(gerr.InvalidArgument, "wal directory is empty")
	}
	opt, err := normalize(opt)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "create wal directory", err)
	}
	names, err := listSegments(dir)
	if err != nil {
		return nil, err
	}
	l := &Log{
		dir:      dir,
		segSize:  opt.SegmentSize,
		group:    opt.GroupCommit,
		archiver: opt.Archiver,
		next:     1,
		fileEnd:  1,
		durable:  1,
	}
	l.cond = sync.NewCond(&l.mu)
	if len(names) == 0 {
		if err := l.createSegment(1, 1); err != nil {
			return nil, err
		}
		return l, nil
	}
	expect := LSN(1)
	for i, name := range names {
		path := filepath.Join(dir, name)
		last := i == len(names)-1
		end, deleted, err := repairSegment(path, uint64(i+1), expect, last)
		if err != nil {
			return nil, err
		}
		if deleted {
			if !last {
				return nil, gerr.Newf(gerr.Corruption, "wal segment %d lost its header", i+1)
			}
			break
		}
		if last {
			if err := l.reopenLast(path, uint64(i+1), expect, end); err != nil {
				return nil, err
			}
		} else {
			l.sealed = append(l.sealed, SegmentInfo{
				Number: uint64(i + 1),
				Path:   path,
				First:  expect,
				End:    end,
			})
		}
		expect = end
	}
	if l.seg == nil {
		num := uint64(1)
		first := LSN(1)
		if n := len(l.sealed); n > 0 {
			num = l.sealed[n-1].Number + 1
			first = l.sealed[n-1].End
		}
		l.next = first
		l.fileEnd = first
		l.durable = first
		if err := l.createSegment(num, first); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func normalize(opt Options) (Options, error) {
	if opt.SegmentSize == 0 {
		opt.SegmentSize = DefaultSegmentSize
	}
	if opt.SegmentSize <= headerSize {
		return Options{}, gerr.Newf(gerr.InvalidArgument, "wal segment size %d is too small", opt.SegmentSize)
	}
	if opt.GroupCommit == 0 {
		opt.GroupCommit = time.Millisecond
	}
	if opt.GroupCommit < 0 {
		return Options{}, gerr.New(gerr.InvalidArgument, "wal group commit delay is negative")
	}
	return opt, nil
}

// Append buffers one record and returns its LSN. The record is durable
// after a later Sync returns nil. payload is copied.
func (l *Log) Append(typ Type, txnID uint64, payload []byte) (LSN, error) {
	if !validType(typ) {
		return 0, gerr.Newf(gerr.InvalidArgument, "wal record type %d is unknown", typ)
	}
	if len(payload) > MaxPayload {
		return 0, gerr.Newf(gerr.InvalidArgument, "wal payload is %d bytes, max %d", len(payload), MaxPayload)
	}
	payload = append([]byte(nil), payload...)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, ErrClosed
	}
	if l.broken != nil {
		return 0, l.broken
	}
	lsn := l.next
	l.buf = appendRecord(l.buf, lsn, txnID, typ, payload)
	l.next += LSN(recordHeader + len(payload) + crcSize)
	return lsn, nil
}

// Sync writes every buffered record and fsyncs them.
// When other Sync calls are already waiting, the leader pauses for the
// group-commit window so the batch shares one fsync. A lone Sync does not wait.
//
// A nil error means every Append that returned before this Sync is durable.
// A non-nil error means the caller must treat the new records as unacknowledged.
// A prefix of that batch may still be on disk; reopen the log to see it.
func (l *Log) Sync() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrClosed
	}
	if l.broken != nil {
		err := l.broken
		l.mu.Unlock()
		return err
	}
	if len(l.buf) == 0 && l.pending == nil && l.durable >= l.next {
		l.mu.Unlock()
		return nil
	}
	if l.flushing {
		ch := make(chan error, 1)
		l.waiters = append(l.waiters, waiter{end: l.next, errc: ch})
		l.cond.Broadcast()
		l.mu.Unlock()
		return <-ch
	}
	l.flushing = true
	l.mu.Unlock()
	return l.flushLeader()
}

func (l *Log) flushLeader() error {
	if d := l.batchDelay(); d > 0 {
		time.Sleep(d)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.flushLocked()
	for _, w := range l.waiters {
		w.errc <- err
	}
	l.waiters = nil
	l.flushing = false
	l.cond.Broadcast()
	return err
}

func (l *Log) batchDelay() time.Duration {
	l.mu.Lock()
	others := len(l.waiters)
	fn := l.delayFn
	window := l.group
	l.mu.Unlock()
	if fn != nil {
		return fn(others + 1)
	}
	if others > 0 && window > 0 {
		return window
	}
	return 0
}

// Durable is the first LSN that is not yet covered by a successful Sync.
// Records with LSN < Durable are on disk.
func (l *Log) Durable() LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.durable
}

// End is the LSN the next Append will assign, including records still buffered.
func (l *Log) End() LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.next
}

// Close syncs buffered records and closes the current segment.
// A second Close returns nil.
func (l *Log) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	syncErr := l.Sync()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return syncErr
	}
	l.closed = true
	var cerr error
	if l.seg != nil {
		cerr = l.seg.Close()
		l.seg = nil
	}
	if syncErr != nil {
		return syncErr
	}
	return cerr
}

// abandon drops the buffer and closes the file without syncing.
// Tests use it as a process crash.
func (l *Log) abandon() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.buf = nil
	l.flushing = false
	for _, w := range l.waiters {
		w.errc <- ErrClosed
	}
	l.waiters = nil
	if l.seg != nil {
		_ = l.seg.Close()
		l.seg = nil
	}
}

func (l *Log) flushLocked() error {
	if err := l.finishPendingLocked(); err != nil {
		return err
	}
	off := 0
	for off < len(l.buf) {
		n, err := encodedLen(l.buf[off:])
		if err != nil {
			l.broken = err
			return err
		}
		if l.mustRotate(n) {
			if err := l.rotateLocked(); err != nil {
				l.buf = append([]byte(nil), l.buf[off:]...)
				return err
			}
		}
		if err := l.writeFull(l.buf[off : off+n]); err != nil {
			l.broken = err
			l.buf = append([]byte(nil), l.buf[off:]...)
			return err
		}
		off += n
	}
	if l.seg == nil {
		l.buf = nil
		l.durable = l.fileEnd
		return nil
	}
	if err := l.syncFile(); err != nil {
		l.broken = err
		l.buf = nil
		return err
	}
	l.buf = nil
	l.durable = l.fileEnd
	return nil
}

func (l *Log) mustRotate(n int) bool {
	if l.seg == nil || l.segLen <= headerSize {
		return false
	}
	return l.segLen+int64(n) > l.segSize
}

func (l *Log) rotateLocked() error {
	if err := l.syncFile(); err != nil {
		l.broken = err
		return err
	}
	l.durable = l.fileEnd
	info := SegmentInfo{
		Number: l.segNum,
		Path:   l.path,
		First:  l.segFirst,
		End:    l.fileEnd,
	}
	l.sealed = append(l.sealed, info)
	if l.seg != nil {
		if err := l.seg.Close(); err != nil {
			l.pending = &info
			l.seg = nil
			l.path = ""
			l.syncedOff = 0
			return err
		}
		l.seg = nil
	}
	l.path = ""
	l.syncedOff = 0
	if l.archiver != nil {
		if err := l.archiver.Archive(info); err != nil {
			l.pending = &info
			return err
		}
	}
	if err := l.createSegment(info.Number+1, info.End); err != nil {
		l.pending = &info
		return err
	}
	return nil
}

func (l *Log) finishPendingLocked() error {
	if l.pending == nil {
		return nil
	}
	info := *l.pending
	if l.archiver != nil {
		if err := l.archiver.Archive(info); err != nil {
			return err
		}
	}
	if l.seg == nil {
		if err := l.createSegment(info.Number+1, info.End); err != nil {
			return err
		}
	}
	l.pending = nil
	return nil
}

func (l *Log) writeFull(p []byte) error {
	if l.seg == nil {
		return gerr.New(gerr.Internal, "wal segment is not open")
	}
	var n int
	var err error
	if l.writeHook != nil {
		n, err = l.writeHook(l.seg, p)
	} else {
		n, err = l.seg.Write(p)
	}
	if n != len(p) {
		if err == nil {
			err = gerr.New(gerr.Unavailable, "short wal write")
		}
		return err
	}
	if err != nil {
		return err
	}
	l.segLen += int64(n)
	l.fileEnd += LSN(n)
	return nil
}

func (l *Log) syncFile() error {
	if l.seg == nil {
		return nil
	}
	var err error
	if l.syncHook != nil {
		err = l.syncHook(l.seg)
	} else {
		err = l.seg.Sync()
	}
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "sync wal segment", err)
	}
	l.syncedOff = l.segLen
	return nil
}

func (l *Log) createSegment(num uint64, first LSN) error {
	path := filepath.Join(l.dir, segmentName(num))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "create wal segment", err)
	}
	hdr := encodeHeader(num, first)
	if _, err := f.Write(hdr); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return gerr.Wrap(gerr.Unavailable, "write wal segment header", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return gerr.Wrap(gerr.Unavailable, "sync wal segment header", err)
	}
	if err := syncDir(l.dir); err != nil {
		_ = f.Close()
		return err
	}
	l.seg = f
	l.path = path
	l.segNum = num
	l.segFirst = first
	l.segLen = headerSize
	l.syncedOff = headerSize
	l.fileEnd = first
	return nil
}

func (l *Log) reopenLast(path string, num uint64, first, end LSN) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "open wal segment", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return gerr.Wrap(gerr.Unavailable, "stat wal segment", err)
	}
	if _, err := f.Seek(info.Size(), io.SeekStart); err != nil {
		_ = f.Close()
		return gerr.Wrap(gerr.Unavailable, "seek wal segment", err)
	}
	l.seg = f
	l.path = path
	l.segNum = num
	l.segFirst = first
	l.segLen = info.Size()
	l.syncedOff = info.Size()
	l.fileEnd = end
	l.next = end
	l.durable = end
	return nil
}

func encodedLen(b []byte) (int, error) {
	if len(b) < recordHeader {
		return 0, gerr.New(gerr.Internal, "buffered wal record is truncated")
	}
	n := int(binary.LittleEndian.Uint32(b[20:24]))
	if n < 0 || n > MaxPayload {
		return 0, gerr.New(gerr.Internal, "buffered wal record has a bad length")
	}
	total := recordHeader + n + crcSize
	if total > len(b) {
		return 0, gerr.New(gerr.Internal, "buffered wal record is truncated")
	}
	return total, nil
}

func segmentName(n uint64) string {
	return fmt.Sprintf("%012d.wal", n)
}

func listSegments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, gerr.Wrap(gerr.Unavailable, "read wal directory", err)
	}
	var names []string
	var nums []uint64
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		n, ok := parseSegmentName(ent.Name())
		if !ok {
			continue
		}
		names = append(names, ent.Name())
		nums = append(nums, n)
	}
	for i := 1; i < len(nums); i++ {
		if nums[i] < nums[i-1] {
			// ReadDir is sorted by name. Fixed-width names sort numerically.
			return nil, gerr.New(gerr.Corruption, "wal segment names are out of order")
		}
	}
	for i, n := range nums {
		if n != uint64(i+1) {
			return nil, gerr.Newf(gerr.Corruption, "wal segment numbers skip %d", i+1)
		}
	}
	return names, nil
}

func parseSegmentName(name string) (uint64, bool) {
	const suffix = ".wal"
	if len(name) != 12+len(suffix) || name[12:] != suffix {
		return 0, false
	}
	for _, c := range name[:12] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(name[:12], 10, 64)
	if err != nil || n == 0 || segmentName(n) != name {
		return 0, false
	}
	return n, true
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "open wal directory", err)
	}
	serr := d.Sync()
	cerr := d.Close()
	if serr != nil {
		return gerr.Wrap(gerr.Unavailable, "sync wal directory", serr)
	}
	if cerr != nil {
		return gerr.Wrap(gerr.Unavailable, "close wal directory", cerr)
	}
	return nil
}

func repairSegment(path string, num uint64, expect LSN, last bool) (end LSN, deleted bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, false, gerr.Wrap(gerr.Unavailable, "open wal segment", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, false, gerr.Wrap(gerr.Unavailable, "stat wal segment", err)
	}
	first, tornHeader, err := readHeader(f, info.Size(), num, expect, last)
	if err != nil {
		return 0, false, err
	}
	if tornHeader {
		if !last {
			return 0, false, gerr.Newf(gerr.Corruption, "wal segment %d has a torn header", num)
		}
		if err := f.Close(); err != nil {
			return 0, false, gerr.Wrap(gerr.Unavailable, "close torn wal segment", err)
		}
		if err := os.Remove(path); err != nil {
			return 0, false, gerr.Wrap(gerr.Unavailable, "remove torn wal segment", err)
		}
		return expect, true, nil
	}
	end, tornAt, err := scanRecords(f, info.Size(), first, last)
	if err != nil {
		return 0, false, err
	}
	if tornAt > 0 {
		if err := f.Truncate(tornAt); err != nil {
			return 0, false, gerr.Wrap(gerr.Unavailable, "truncate torn wal tail", err)
		}
		if err := f.Sync(); err != nil {
			return 0, false, gerr.Wrap(gerr.Unavailable, "sync truncated wal segment", err)
		}
	}
	return end, false, nil
}

func readHeader(f *os.File, size int64, num uint64, expect LSN, allowTorn bool) (first LSN, torn bool, err error) {
	n := headerSize
	if int64(n) > size {
		n = int(size)
	}
	buf := make([]byte, n)
	if n > 0 {
		if err := readFullAt(f, buf, 0); err != nil && !errors.Is(err, io.EOF) {
			return 0, false, gerr.Wrap(gerr.Unavailable, "read wal header", err)
		}
	}
	if len(buf) >= 4 && string(buf[:4]) != Magic {
		return 0, false, gerr.New(gerr.InvalidArgument, "wal segment has a bad magic")
	}
	if len(buf) >= 6 {
		version := binary.LittleEndian.Uint16(buf[4:6])
		if version > FormatVersion {
			return 0, false, gerr.Newf(gerr.InvalidArgument, "wal format version %d is newer than supported version %d", version, FormatVersion)
		}
		if version != FormatVersion && (len(buf) >= headerSize || version != 0) {
			return 0, false, gerr.Newf(gerr.InvalidArgument, "wal format version %d is not supported (this build reads version %d)", version, FormatVersion)
		}
	}
	if len(buf) < headerSize {
		if !allowTorn {
			return 0, false, gerr.Newf(gerr.Corruption, "wal segment %d has a short header", num)
		}
		return 0, true, nil
	}
	sum := crc32.Checksum(buf[:24], crcTable)
	got := binary.LittleEndian.Uint32(buf[24:28])
	if sum != got {
		// A header-sized file whose checksum did not land is a torn create.
		// A longer file has records behind that header, so a bad checksum is corruption.
		if allowTorn && size == headerSize {
			return 0, true, nil
		}
		return 0, false, gerr.Newf(gerr.Corruption, "wal segment %d checksum mismatch", num)
	}
	fileNum := binary.LittleEndian.Uint64(buf[8:16])
	if fileNum != num {
		return 0, false, gerr.Newf(gerr.Corruption, "wal segment %d is numbered %d", num, fileNum)
	}
	first = LSN(binary.LittleEndian.Uint64(buf[16:24]))
	if first != expect {
		return 0, false, gerr.Newf(gerr.Corruption, "wal segment %d starts at lsn %d, want %d", num, first, expect)
	}
	return first, false, nil
}

func scanRecords(f *os.File, fileSize int64, first LSN, allowTorn bool) (end LSN, tornAt int64, err error) {
	off := int64(headerSize)
	lsn := first
	for off < fileSize {
		remain := fileSize - off
		if remain < recordHeader {
			if !allowTorn {
				return 0, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d is truncated", lsn)
			}
			return lsn, off, nil
		}
		var hdr [recordHeader]byte
		if err := readFullAt(f, hdr[:], off); err != nil {
			return 0, 0, gerr.Wrap(gerr.Unavailable, "read wal record", err)
		}
		payLen := binary.LittleEndian.Uint32(hdr[20:24])
		if payLen > MaxPayload {
			if !allowTorn {
				return 0, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d has length %d", lsn, payLen)
			}
			return lsn, off, nil
		}
		total := int64(recordHeader) + int64(payLen) + crcSize
		if off+total > fileSize {
			if !allowTorn {
				return 0, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d is truncated", lsn)
			}
			return lsn, off, nil
		}
		buf := make([]byte, total)
		if err := readFullAt(f, buf, off); err != nil {
			return 0, 0, gerr.Wrap(gerr.Unavailable, "read wal record", err)
		}
		sum := crc32.Checksum(buf[:len(buf)-crcSize], crcTable)
		got := binary.LittleEndian.Uint32(buf[len(buf)-crcSize:])
		if sum != got {
			return 0, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d checksum mismatch", lsn)
		}
		recLSN := LSN(binary.LittleEndian.Uint64(buf[0:8]))
		if recLSN != lsn {
			return 0, 0, gerr.Newf(gerr.Corruption, "wal record lsn %d, want %d", recLSN, lsn)
		}
		typ := Type(binary.LittleEndian.Uint16(buf[16:18]))
		if !validType(typ) {
			return 0, 0, gerr.Newf(gerr.Corruption, "wal record at lsn %d has type %d", lsn, typ)
		}
		lsn += LSN(total)
		off += total
	}
	return lsn, 0, nil
}

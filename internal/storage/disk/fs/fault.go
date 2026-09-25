package fs

import (
	"io"
	"io/fs"
	"sync"
)

// Fault wraps a File and injects failures.
// A nil Fault method is not usable; construct one with Wrap.
//
// Reordering queues WriteAt calls until Sync, Flush, or Close. ReadAt of a
// queued range returns those bytes, so a caller sees its own writes. Discard
// drops the queue; that is the crash. ReversePending changes the order Sync
// will apply.
//
// One-shot faults (FailNext*, ShortWrite) apply to the next matching call
// and then clear, including when reordering is on. A short write is sent to
// the inner file immediately and is not queued.
type Fault struct {
	mu     sync.Mutex
	inner  File
	closed bool

	reorder bool
	pending []queued

	failRead  error
	failWrite error
	failSync  error
	shortOn   bool
	shortN    int
}

type queued struct {
	off  int64
	data []byte
}

// Wrap returns a Fault that delegates to inner until a fault is armed.
func Wrap(inner File) *Fault {
	return &Fault{inner: inner}
}

// SetReorder queues writes when on is true and sends them straight through
// when on is false. Turning it off does not flush the queue.
func (f *Fault) SetReorder(on bool) {
	f.mu.Lock()
	f.reorder = on
	f.mu.Unlock()
}

// FailNextRead makes the next ReadAt return err.
func (f *Fault) FailNextRead(err error) {
	f.mu.Lock()
	f.failRead = err
	f.mu.Unlock()
}

// FailNextWrite makes the next WriteAt return err without writing.
func (f *Fault) FailNextWrite(err error) {
	f.mu.Lock()
	f.failWrite = err
	f.mu.Unlock()
}

// FailNextSync makes the next Sync return err. Queued writes stay queued.
func (f *Fault) FailNextSync(err error) {
	f.mu.Lock()
	f.failSync = err
	f.mu.Unlock()
}

// ShortWrite makes the next WriteAt persist at most n bytes and return
// io.ErrShortWrite when n is less than the buffer. n is clamped to the
// buffer length. The write is not queued.
func (f *Fault) ShortWrite(n int) {
	f.mu.Lock()
	f.shortOn = true
	f.shortN = n
	f.mu.Unlock()
}

// Pending is the number of queued writes.
func (f *Fault) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

// ReversePending reverses the queued write order.
func (f *Fault) ReversePending() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, j := 0, len(f.pending)-1; i < j; i, j = i+1, j-1 {
		f.pending[i], f.pending[j] = f.pending[j], f.pending[i]
	}
}

// Discard drops queued writes. Bytes already sent to the inner file stay.
func (f *Fault) Discard() {
	f.mu.Lock()
	f.pending = nil
	f.mu.Unlock()
}

// Flush applies queued writes in queue order.
func (f *Fault) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushLocked()
}

func (f *Fault) flushLocked() error {
	for _, rec := range f.pending {
		if err := writeFull(f.inner, rec.data, rec.off); err != nil {
			return err
		}
	}
	f.pending = nil
	return nil
}

// ReadAt reads from the inner file, then overlays queued writes.
func (f *Fault) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.failRead != nil {
		err := f.failRead
		f.failRead = nil
		return 0, err
	}
	n, err := f.inner.ReadAt(p, off)
	if !f.reorder || len(f.pending) == 0 {
		return n, err
	}
	known := make([]bool, len(p))
	limit := n
	if limit > len(p) {
		limit = len(p)
	}
	for i := 0; i < limit; i++ {
		known[i] = true
	}
	for _, rec := range f.pending {
		overlay(p, off, known, rec)
	}
	if len(p) > 0 && allKnown(known) {
		return len(p), nil
	}
	return n, err
}

// WriteAt writes, queues, or fails, depending on the armed fault.
func (f *Fault) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.failWrite != nil {
		err := f.failWrite
		f.failWrite = nil
		return 0, err
	}
	if f.shortOn {
		f.shortOn = false
		n := f.shortN
		if n < 0 {
			n = 0
		}
		if n > len(p) {
			n = len(p)
		}
		if n > 0 {
			wrote, err := f.inner.WriteAt(p[:n], off)
			if err != nil {
				return wrote, err
			}
			n = wrote
		}
		if n < len(p) {
			return n, io.ErrShortWrite
		}
		return n, nil
	}
	if f.reorder {
		cp := make([]byte, len(p))
		copy(cp, p)
		f.pending = append(f.pending, queued{off: off, data: cp})
		return len(p), nil
	}
	return f.inner.WriteAt(p, off)
}

// Sync applies the queue, then syncs the inner file.
// FailNextSync returns before applying the queue.
func (f *Fault) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fs.ErrClosed
	}
	if f.failSync != nil {
		err := f.failSync
		f.failSync = nil
		return err
	}
	if err := f.flushLocked(); err != nil {
		return err
	}
	return f.inner.Sync()
}

// Truncate truncates the inner file. Queued writes are left queued.
func (f *Fault) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fs.ErrClosed
	}
	return f.inner.Truncate(size)
}

// Close flushes queued writes and closes the inner file.
func (f *Fault) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	flushErr := f.flushLocked()
	closeErr := f.inner.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// Stat returns the inner file's info and does not account for queued writes.
func (f *Fault) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fs.ErrClosed
	}
	return f.inner.Stat()
}

func overlay(dst []byte, dstOff int64, known []bool, rec queued) {
	d1 := dstOff + int64(len(dst))
	r1 := rec.off + int64(len(rec.data))
	lo := dstOff
	if rec.off > lo {
		lo = rec.off
	}
	hi := d1
	if r1 < hi {
		hi = r1
	}
	if lo >= hi {
		return
	}
	copy(dst[lo-dstOff:hi-dstOff], rec.data[lo-rec.off:hi-rec.off])
	for i := lo; i < hi; i++ {
		known[i-dstOff] = true
	}
}

func allKnown(known []bool) bool {
	for _, ok := range known {
		if !ok {
			return false
		}
	}
	return true
}

func writeFull(w interface {
	WriteAt(p []byte, off int64) (int, error)
}, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := w.WriteAt(p, off)
		if n > 0 {
			p = p[n:]
			off += int64(n)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

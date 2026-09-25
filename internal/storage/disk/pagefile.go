package disk

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk/fs"
)

// ErrClosed is returned when a method is called on a closed page file or pool.
var ErrClosed = errors.New("disk: closed")

// Options configures Create and Open.
// Page size is fixed at Create. Open rejects a file whose version is newer
// than FormatVersion, and a file whose page size does not match PageSize
// when PageSize is non-zero.
type Options struct {
	// PageSize is the page size in bytes. Zero selects DefaultPageSize on
	// Create and the size stored in the file on Open.
	PageSize int
	// UUID is written at Create. The zero value generates a random id.
	UUID [16]byte
	// Created is written at Create. The zero value uses the current time.
	Created time.Time
	// File, when set, is used instead of opening a path. The page file owns
	// it and closes it. Tests pass a fault-injecting file here.
	File fs.File
}

// Meta is a copy of the file header.
type Meta struct {
	Version       uint16
	PageSize      int
	UUID          [16]byte
	Created       time.Time
	CheckpointLSN uint64
	FreelistHead  PageID
	PageCount     uint64
	FreePages     uint64
	Roots         map[storage.Keyspace]PageID
}

// PageInfo is a copy of one page. Data is the payload only: the page header
// and checksum are not included, and the slice does not alias the file.
type PageInfo struct {
	ID   PageID
	LSN  uint64
	Type PageType
	Data []byte
}

// PageFile is one heap file. Page 0 is the header. Pages are allocated from
// the freelist or by extending the file. Methods are safe for concurrent use.
type PageFile struct {
	mu       sync.Mutex
	f        fs.File
	path     string
	pageSize int
	meta     fileMeta
	closed   bool
}

// Create writes a new page file at path. The file must not already exist
// unless Options.File is set, in which case that file is truncated to one
// header page.
func Create(path string, opts Options) (*PageFile, error) {
	ps := opts.PageSize
	if ps == 0 {
		ps = DefaultPageSize
	}
	if err := checkPageSize(ps); err != nil {
		return nil, err
	}
	file, err := openOS(path, opts.File, true)
	if err != nil {
		return nil, err
	}
	id := opts.UUID
	if id == ([16]byte{}) {
		if _, err := rand.Read(id[:]); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	created := opts.Created
	if created.IsZero() {
		created = time.Now()
	}
	pf := &PageFile{
		f:        file,
		path:     path,
		pageSize: ps,
		meta: fileMeta{
			version:   FormatVersion,
			pageSize:  ps,
			uuid:      id,
			created:   created.UnixNano(),
			pageCount: 1,
		},
	}
	if err := pf.writeHeaderLocked(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Truncate(int64(ps)); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return pf, nil
}

// Open checks the header and freelist of an existing page file.
// A format version newer than FormatVersion is refused before the checksum
// is consulted, so a later layout can still be recognized as too new.
func Open(path string, opts Options) (*PageFile, error) {
	file, err := openOS(path, opts.File, false)
	if err != nil {
		return nil, err
	}
	pf, err := openFile(path, file, opts.PageSize)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return pf, nil
}

func openOS(path string, file fs.File, create bool) (fs.File, error) {
	if file != nil {
		return file, nil
	}
	if path == "" {
		return nil, gerr.New(gerr.InvalidArgument, "page file path is empty")
	}
	flag := os.O_RDWR
	if create {
		flag |= os.O_CREATE | os.O_EXCL
	}
	f, err := fs.OpenFile(path, flag, 0o644)
	if err != nil {
		if create && os.IsExist(err) {
			return nil, gerr.Wrap(gerr.AlreadyExists, path, err)
		}
		if os.IsNotExist(err) {
			return nil, gerr.Wrap(gerr.NotFound, path, err)
		}
		return nil, err
	}
	return f, nil
}

func openFile(path string, file fs.File, wantSize int) (*PageFile, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < int64(pageHeaderSize+offPageSize+4) {
		return nil, corruptionf("page file %s is truncated", path)
	}
	probe := make([]byte, pageHeaderSize+offPageSize+4)
	if err := readFull(file, probe, 0); err != nil {
		return nil, err
	}
	if string(probe[pageHeaderSize+offMagic:pageHeaderSize+offMagic+4]) != Magic {
		return nil, gerr.Newf(gerr.InvalidArgument, "file %s is not a graphdb page file", path)
	}
	version := le16(probe[pageHeaderSize+offVersion:])
	if version > FormatVersion {
		return nil, gerr.Newf(gerr.InvalidArgument, "page file format version %d is newer than supported version %d", version, FormatVersion)
	}
	if version != FormatVersion {
		return nil, gerr.Newf(gerr.InvalidArgument, "page file format version %d is not supported (this build reads version %d)", version, FormatVersion)
	}
	ps := int(le32(probe[pageHeaderSize+offPageSize:]))
	if err := checkPageSize(ps); err != nil {
		return nil, corruptionf("page file %s has invalid page size %d", path, ps)
	}
	if wantSize != 0 && wantSize != ps {
		return nil, gerr.Newf(gerr.InvalidArgument, "page file page size %d does not match requested %d", ps, wantSize)
	}
	if info.Size() < int64(ps) {
		return nil, corruptionf("page file %s is shorter than one page", path)
	}
	page, err := readPageAt(file, ps, 0)
	if err != nil {
		return nil, err
	}
	if pageIDOf(page) != 0 || pageTypeOf(page) != TypeHeader {
		return nil, corruptionf("page 0 is not the file header")
	}
	meta, err := decodeMeta(payload(page))
	if err != nil {
		return nil, err
	}
	if meta.version != FormatVersion || meta.pageSize != ps {
		return nil, corruptionf("file header does not match the page prefix")
	}
	if meta.uuid == ([16]byte{}) {
		return nil, corruptionf("file header has no database id")
	}
	if meta.pageCount < 1 {
		return nil, corruptionf("file header page count is zero")
	}
	need := int64(meta.pageCount) * int64(ps)
	if need/int64(ps) != int64(meta.pageCount) || info.Size() < need {
		return nil, corruptionf("page file is %d bytes, header requires %d", info.Size(), need)
	}
	pf := &PageFile{
		f:        file,
		path:     path,
		pageSize: ps,
		meta:     meta,
	}
	if err := pf.checkFreelistLocked(); err != nil {
		return nil, err
	}
	return pf, nil
}

func le16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// Close syncs and closes the file. A second Close is a no-op.
func (f *PageFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	syncErr := f.f.Sync()
	closeErr := f.f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Sync flushes the file. It does not write dirty buffer-pool pages; the pool
// flushes those itself.
func (f *PageFile) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	return f.f.Sync()
}

// PageSize is the page size in bytes.
func (f *PageFile) PageSize() int { return f.pageSize }

// PayloadSize is the number of bytes ReadPage and WritePage accept.
func (f *PageFile) PayloadSize() int { return f.pageSize - pageOverhead }

// PageCount includes the header page.
func (f *PageFile) PageCount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta.pageCount
}

// Meta returns a copy of the header fields.
func (f *PageFile) Meta() Meta {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.metaCopy()
}

func (f *PageFile) metaCopy() Meta {
	roots := make(map[storage.Keyspace]PageID, len(rootOrder))
	for i, ks := range rootOrder {
		roots[ks] = f.meta.roots[i]
	}
	return Meta{
		Version:       f.meta.version,
		PageSize:      f.meta.pageSize,
		UUID:          f.meta.uuid,
		Created:       time.Unix(0, f.meta.created).UTC(),
		CheckpointLSN: f.meta.checkpoint,
		FreelistHead:  f.meta.freelist,
		PageCount:     f.meta.pageCount,
		FreePages:     f.meta.freeCount,
		Roots:         roots,
	}
}

// CheckpointLSN is the last checkpoint LSN stored in the header. Phase 2E
// advances it. This phase only stores the value.
func (f *PageFile) CheckpointLSN() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta.checkpoint
}

// SetCheckpointLSN records lsn in the header. The new header is durable after
// Sync.
func (f *PageFile) SetCheckpointLSN(lsn uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	prev := f.meta.checkpoint
	f.meta.checkpoint = lsn
	if err := f.writeHeaderLocked(); err != nil {
		f.meta.checkpoint = prev
		return err
	}
	return nil
}

// Root returns the stored root page for ks. Zero means the tree has not been
// created.
func (f *PageFile) Root(ks storage.Keyspace) (PageID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	slot, err := rootSlot(ks)
	if err != nil {
		return 0, err
	}
	return f.meta.roots[slot], nil
}

// SetRoot records id as the root page for ks. Zero clears it. A non-zero id
// must be an allocated page that is not on the freelist.
func (f *PageFile) SetRoot(ks storage.Keyspace, id PageID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	slot, err := rootSlot(ks)
	if err != nil {
		return err
	}
	if id != 0 {
		if uint64(id) >= f.meta.pageCount {
			return gerr.Newf(gerr.InvalidArgument, "root page %d is not allocated", id)
		}
		page, err := f.readPageLocked(id)
		if err != nil {
			return err
		}
		if pageTypeOf(page) == TypeFree {
			return gerr.Newf(gerr.InvalidArgument, "root page %d is free", id)
		}
	}
	prev := f.meta.roots[slot]
	f.meta.roots[slot] = id
	if err := f.writeHeaderLocked(); err != nil {
		f.meta.roots[slot] = prev
		return err
	}
	return nil
}

// ReadPage returns a copy of page id. The page must already be allocated.
// Free pages are readable so a caller can see the freelist link.
func (f *PageFile) ReadPage(id PageID) (PageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return PageInfo{}, ErrClosed
	}
	page, err := f.readPageLocked(id)
	if err != nil {
		return PageInfo{}, err
	}
	raw := payload(page)
	data := make([]byte, len(raw))
	copy(data, raw)
	return PageInfo{
		ID:   pageIDOf(page),
		LSN:  pageLSNOf(page),
		Type: pageTypeOf(page),
		Data: data[:len(data):len(data)],
	}, nil
}

// WritePage replaces the payload of an allocated data page and keeps the
// page LSN. Page 0 and free pages are rejected. len(data) must be PayloadSize.
func (f *PageFile) WritePage(id PageID, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if id == 0 {
		return gerr.New(gerr.InvalidArgument, "page 0 is the file header")
	}
	if len(data) != f.pageSize-pageOverhead {
		return gerr.Newf(gerr.InvalidArgument, "payload length %d, want %d", len(data), f.pageSize-pageOverhead)
	}
	page, err := f.readPageLocked(id)
	if err != nil {
		return err
	}
	switch pageTypeOf(page) {
	case TypeFree:
		return gerr.Newf(gerr.InvalidArgument, "page %d is free", id)
	case TypeHeader:
		return gerr.New(gerr.InvalidArgument, "page 0 is the file header")
	}
	copy(payload(page), data)
	setPageType(page, TypeData)
	seal(page)
	return f.writeAtFull(page, f.offset(id))
}

// Allocate returns a zeroed data page. A free page is reused when the
// freelist is not empty; otherwise the file grows by one page.
func (f *PageFile) Allocate() (PageID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ErrClosed
	}
	if f.meta.freelist != 0 {
		if f.meta.freeCount == 0 {
			return 0, corruptionf("freelist head %d is set but the free count is zero", f.meta.freelist)
		}
		return f.allocFromFreelist()
	}
	return f.allocExtend()
}

// allocFromFreelist unlinks the head before overwriting it. A crash between
// the header write and the data write leaks a free page. It does not hand
// that page out twice.
func (f *PageFile) allocFromFreelist() (PageID, error) {
	id := f.meta.freelist
	page, err := f.readPageLocked(id)
	if err != nil {
		return 0, err
	}
	if pageTypeOf(page) != TypeFree {
		return 0, corruptionf("freelist head %d is not a free page", id)
	}
	next := freeNext(page)
	prevHead, prevFree := f.meta.freelist, f.meta.freeCount
	f.meta.freelist = next
	f.meta.freeCount--
	if err := f.writeHeaderLocked(); err != nil {
		f.meta.freelist, f.meta.freeCount = prevHead, prevFree
		return 0, err
	}
	fresh := blankPage(f.pageSize, id, TypeData, 0)
	if err := f.writeAtFull(fresh, f.offset(id)); err != nil {
		f.meta.freelist, f.meta.freeCount = prevHead, prevFree
		if herr := f.writeHeaderLocked(); herr != nil {
			return 0, errors.Join(err, herr)
		}
		return 0, err
	}
	return id, nil
}

// allocExtend writes the new page before publishing the higher page count.
// A crash in between leaves a tail the next open ignores.
func (f *PageFile) allocExtend() (PageID, error) {
	id := PageID(f.meta.pageCount)
	off, err := f.offsetChecked(id)
	if err != nil {
		return 0, err
	}
	fresh := blankPage(f.pageSize, id, TypeData, 0)
	if err := f.writeAtFull(fresh, off); err != nil {
		return 0, err
	}
	prev := f.meta.pageCount
	f.meta.pageCount++
	if err := f.writeHeaderLocked(); err != nil {
		f.meta.pageCount = prev
		return 0, err
	}
	return id, nil
}

// Free pushes id onto the freelist. The free page is written before the
// header points at it, so a crash leaks the page instead of treating live
// bytes as free. Page 0, a tree root, and a page that is already free are
// rejected.
func (f *PageFile) Free(id PageID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if id == 0 {
		return gerr.New(gerr.InvalidArgument, "cannot free the header page")
	}
	if uint64(id) >= f.meta.pageCount {
		return gerr.Newf(gerr.InvalidArgument, "page %d is not allocated", id)
	}
	for _, root := range f.meta.roots {
		if root == id {
			return gerr.Newf(gerr.InvalidArgument, "page %d is a tree root", id)
		}
	}
	page, err := f.readPageLocked(id)
	if err != nil {
		return err
	}
	switch pageTypeOf(page) {
	case TypeFree:
		return gerr.Newf(gerr.InvalidArgument, "page %d is already free", id)
	case TypeHeader:
		return gerr.New(gerr.InvalidArgument, "cannot free the header page")
	}
	freed := blankPage(f.pageSize, id, TypeFree, 0)
	setFreeNext(freed, f.meta.freelist)
	seal(freed)
	if err := f.writeAtFull(freed, f.offset(id)); err != nil {
		return err
	}
	prevHead, prevFree := f.meta.freelist, f.meta.freeCount
	f.meta.freelist = id
	f.meta.freeCount++
	if err := f.writeHeaderLocked(); err != nil {
		f.meta.freelist, f.meta.freeCount = prevHead, prevFree
		return err
	}
	return nil
}

// Truncate drops pages at and above pageCount. Every dropped page must
// already be free, and no root may point at one. The header is published
// before the file is shortened.
func (f *PageFile) Truncate(pageCount uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if pageCount < 1 {
		return gerr.New(gerr.InvalidArgument, "truncate would drop the header page")
	}
	if pageCount > f.meta.pageCount {
		return gerr.Newf(gerr.InvalidArgument, "truncate page count %d is past the file end %d", pageCount, f.meta.pageCount)
	}
	if pageCount == f.meta.pageCount {
		return f.truncateFileLocked(pageCount)
	}
	for _, ks := range rootOrder {
		slot, _ := rootSlot(ks)
		if root := f.meta.roots[slot]; root != 0 && uint64(root) >= pageCount {
			return gerr.Newf(gerr.InvalidArgument, "root page %d would be truncated", root)
		}
	}
	links, err := f.freelistSnapshot()
	if err != nil {
		return err
	}
	keep, drop := splitLinks(links, pageCount)
	for id := pageCount; id < f.meta.pageCount; id++ {
		if _, ok := drop[PageID(id)]; !ok {
			return gerr.Newf(gerr.InvalidArgument, "page %d is still live", id)
		}
	}
	if err := f.relinkFreelist(keep); err != nil {
		if rerr := f.restoreFreelist(links); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	prev := f.meta
	f.meta.pageCount = pageCount
	if len(keep) == 0 {
		f.meta.freelist = 0
	} else {
		f.meta.freelist = keep[0]
	}
	f.meta.freeCount = uint64(len(keep))
	if err := f.writeHeaderLocked(); err != nil {
		f.meta = prev
		if rerr := f.restoreFreelist(links); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	return f.truncateFileLocked(pageCount)
}

func (f *PageFile) truncateFileLocked(pageCount uint64) error {
	size := int64(pageCount) * int64(f.pageSize)
	if f.pageSize == 0 || size/int64(f.pageSize) != int64(pageCount) {
		return gerr.Newf(gerr.InvalidArgument, "truncate of %d pages overflows", pageCount)
	}
	if err := f.f.Truncate(size); err != nil {
		return gerr.Wrap(gerr.Unavailable, "truncate page file", err)
	}
	return nil
}

type freelistLink struct {
	id   PageID
	next PageID
}

func (f *PageFile) freelistSnapshot() ([]freelistLink, error) {
	var links []freelistLink
	seen := map[PageID]struct{}{}
	id := f.meta.freelist
	for id != 0 {
		if _, ok := seen[id]; ok {
			return nil, corruptionf("freelist cycle at page %d", id)
		}
		seen[id] = struct{}{}
		page, err := f.readPageLocked(id)
		if err != nil {
			return nil, err
		}
		next := freeNext(page)
		links = append(links, freelistLink{id: id, next: next})
		id = next
		if uint64(len(seen)) > f.meta.pageCount {
			return nil, corruptionf("freelist is longer than the file")
		}
	}
	return links, nil
}

func splitLinks(links []freelistLink, pageCount uint64) (keep []PageID, drop map[PageID]struct{}) {
	drop = map[PageID]struct{}{}
	for _, link := range links {
		if uint64(link.id) >= pageCount {
			drop[link.id] = struct{}{}
			continue
		}
		keep = append(keep, link.id)
	}
	return keep, drop
}

func (f *PageFile) restoreFreelist(links []freelistLink) error {
	for _, link := range links {
		if uint64(link.id) >= f.meta.pageCount {
			continue
		}
		page := blankPage(f.pageSize, link.id, TypeFree, 0)
		setFreeNext(page, link.next)
		seal(page)
		if err := f.writeAtFull(page, f.offset(link.id)); err != nil {
			return err
		}
	}
	return nil
}

func (f *PageFile) relinkFreelist(keep []PageID) error {
	for i, id := range keep {
		var next PageID
		if i+1 < len(keep) {
			next = keep[i+1]
		}
		page := blankPage(f.pageSize, id, TypeFree, 0)
		setFreeNext(page, next)
		seal(page)
		if err := f.writeAtFull(page, f.offset(id)); err != nil {
			return err
		}
	}
	return nil
}

func (f *PageFile) checkFreelistLocked() error {
	seen := map[PageID]struct{}{}
	id := f.meta.freelist
	var n uint64
	for id != 0 {
		if _, ok := seen[id]; ok {
			return corruptionf("freelist cycle at page %d", id)
		}
		if uint64(id) >= f.meta.pageCount {
			return corruptionf("freelist page %d is past page count %d", id, f.meta.pageCount)
		}
		seen[id] = struct{}{}
		page, err := f.readPageLocked(id)
		if err != nil {
			return err
		}
		if pageTypeOf(page) != TypeFree {
			return corruptionf("freelist page %d has type %d", id, pageTypeOf(page))
		}
		id = freeNext(page)
		n++
		if n > f.meta.pageCount {
			return corruptionf("freelist is longer than the file")
		}
	}
	if n != f.meta.freeCount {
		return corruptionf("freelist has %d pages, header says %d", n, f.meta.freeCount)
	}
	return nil
}

func (f *PageFile) readPageLocked(id PageID) ([]byte, error) {
	if uint64(id) >= f.meta.pageCount {
		return nil, gerr.Newf(gerr.NotFound, "page %d is not allocated", id)
	}
	page, err := readPageAt(f.f, f.pageSize, f.offset(id))
	if err != nil {
		return nil, err
	}
	if pageIDOf(page) != id {
		return nil, corruptionf("page %d header says id %d", id, pageIDOf(page))
	}
	return page, nil
}

func readPageAt(file fs.File, pageSize int, off int64) ([]byte, error) {
	page := make([]byte, pageSize)
	if err := readFull(file, page, off); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, corruptionf("short read at offset %d", off)
		}
		return nil, err
	}
	if err := verifyChecksum(page); err != nil {
		return nil, err
	}
	return page, nil
}

func (f *PageFile) writeHeaderLocked() error {
	page := blankPage(f.pageSize, 0, TypeHeader, 0)
	encodeMeta(payload(page), f.meta)
	seal(page)
	return f.writeAtFull(page, 0)
}

func (f *PageFile) writeImage(page []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if len(page) != f.pageSize {
		return gerr.Newf(gerr.InvalidArgument, "page image length %d, want %d", len(page), f.pageSize)
	}
	id := pageIDOf(page)
	if id == 0 || uint64(id) >= f.meta.pageCount {
		return gerr.Newf(gerr.InvalidArgument, "page %d is not a data page", id)
	}
	seal(page)
	return f.writeAtFull(page, f.offset(id))
}

func (f *PageFile) readRaw(id PageID) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, ErrClosed
	}
	return f.readPageLocked(id)
}

func (f *PageFile) offset(id PageID) int64 {
	return int64(id) * int64(f.pageSize)
}

func (f *PageFile) offsetChecked(id PageID) (int64, error) {
	if f.pageSize == 0 || int64(id) > (1<<62)/int64(f.pageSize) {
		return 0, gerr.Newf(gerr.InvalidArgument, "page %d offset overflows", id)
	}
	return f.offset(id), nil
}

func (f *PageFile) writeAtFull(p []byte, off int64) error {
	if err := writeFull(f.f, p, off); err != nil {
		return gerr.Wrap(gerr.Unavailable, "write page", err)
	}
	return nil
}

func readFull(file fs.File, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := file.ReadAt(p, off)
		if n > 0 {
			p = p[n:]
			off += int64(n)
		}
		if len(p) == 0 {
			return nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func writeFull(file fs.File, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := file.WriteAt(p, off)
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

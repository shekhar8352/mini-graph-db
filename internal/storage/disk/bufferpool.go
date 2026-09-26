package disk

import (
	"errors"
	"sync"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// ErrUnpinned is returned when a page handle is used after Unpin or Free.
var ErrUnpinned = errors.New("disk: page is not pinned")

// PoolHooks reports buffer-pool events. Hooks run without the pool lock and
// must not call back into the pool or the page file.
type PoolHooks struct {
	Hit   func(id PageID)
	Miss  func(id PageID)
	Evict func(id PageID)
}

// PoolStats counts cache traffic since the pool was opened.
type PoolStats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Flushes   uint64
	Frames    int
	Cached    int
	Dirty     int
	Pins      int
}

// HitRatio is hits / (hits + misses). An unused pool reports 0.
func (s PoolStats) HitRatio() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// Pool is a fixed table of page frames. A frame is evicted only when its pin
// count is zero, in least-recently-unpinned order. Dirty frames are written
// back before their slot is reused and when FlushAll or Close runs.
//
// The pool is the only writer of pages it caches. Page 0 is the file header
// and is not cached. A Page handle must be used from one goroutine; different
// handles may be used concurrently.
type Pool struct {
	mu        sync.Mutex
	file      *PageFile
	frames    []frame
	index     map[PageID]int
	free      []int
	lruHead   int
	lruTail   int
	dirtyHead int
	hooks     PoolHooks
	hits      uint64
	misses    uint64
	evictions uint64
	flushes   uint64
	closed    bool
}

type frame struct {
	id        PageID
	pin       int
	dirty     bool
	used      bool
	buf       []byte
	lruPrev   int
	lruNext   int
	dirtyPrev int
	dirtyNext int
}

// Page is a pinned frame. Data returns the payload bytes. The caller marks
// the page dirty after changing them. The slice is invalid after Unpin.
type Page struct {
	pool *Pool
	idx  int
	id   PageID
	live bool
}

// NewPool returns a pool with n frames over file. n must be at least 1.
func NewPool(file *PageFile, n int) (*Pool, error) {
	if file == nil {
		return nil, gerr.New(gerr.InvalidArgument, "buffer pool file is nil")
	}
	if n < 1 {
		return nil, gerr.New(gerr.InvalidArgument, "buffer pool needs at least one frame")
	}
	frames := make([]frame, n)
	free := make([]int, n)
	for i := range frames {
		frames[i].lruPrev = -1
		frames[i].lruNext = -1
		frames[i].dirtyPrev = -1
		frames[i].dirtyNext = -1
		free[i] = i
	}
	return &Pool{
		file:      file,
		frames:    frames,
		index:     map[PageID]int{},
		free:      free,
		lruHead:   -1,
		lruTail:   -1,
		dirtyHead: -1,
	}, nil
}

// SetHooks replaces the metrics hooks.
func (p *Pool) SetHooks(h PoolHooks) {
	p.mu.Lock()
	p.hooks = h
	p.mu.Unlock()
}

// Stats returns a snapshot of counters and current occupancy.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := PoolStats{
		Hits:      p.hits,
		Misses:    p.misses,
		Evictions: p.evictions,
		Flushes:   p.flushes,
		Frames:    len(p.frames),
		Cached:    len(p.index),
	}
	for i := range p.frames {
		s.Pins += p.frames[i].pin
		if p.frames[i].dirty {
			s.Dirty++
		}
	}
	return s
}

// Get pins page id, loading it if needed. The caller must Unpin.
func (p *Pool) Get(id PageID) (*Page, error) {
	p.mu.Lock()
	pg, evicted, hit, err := p.pin(id, false)
	hooks := p.hooks
	p.mu.Unlock()
	fire(hooks, id, evicted, hit, err)
	return pg, err
}

// Alloc allocates a zeroed page and pins it.
func (p *Pool) Alloc() (*Page, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	id, err := p.file.Allocate()
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	pg, evicted, _, err := p.pin(id, true)
	if err != nil {
		if ferr := p.file.Free(id); ferr != nil {
			err = errors.Join(err, ferr)
		}
		p.mu.Unlock()
		return nil, err
	}
	evict := p.hooks.Evict
	p.mu.Unlock()
	if evicted != 0 && evict != nil {
		evict(evicted)
	}
	return pg, nil
}

// pin loads id if needed and returns a pinned handle.
// alloc is true when the page was just allocated: that load is not a miss
// from the caller's point of view, and it is not a hit either.
func (p *Pool) pin(id PageID, alloc bool) (*Page, PageID, bool, error) {
	if p.closed {
		return nil, 0, false, ErrClosed
	}
	if id == 0 {
		return nil, 0, false, gerr.New(gerr.InvalidArgument, "page 0 is the file header")
	}
	if idx, ok := p.index[id]; ok {
		p.pinFrame(idx)
		if !alloc {
			p.hits++
		}
		return &Page{pool: p, idx: idx, id: id, live: true}, 0, true, nil
	}
	if uint64(id) >= p.file.PageCount() {
		return nil, 0, false, gerr.Newf(gerr.NotFound, "page %d is not allocated", id)
	}
	img, err := p.file.readRaw(id)
	if err != nil {
		return nil, 0, false, err
	}
	if pageTypeOf(img) == TypeFree {
		return nil, 0, false, gerr.Newf(gerr.InvalidArgument, "page %d is free", id)
	}
	idx, evicted, err := p.acquireFrame()
	if err != nil {
		return nil, 0, false, err
	}
	p.install(idx, id, img)
	if !alloc {
		p.misses++
	}
	return &Page{pool: p, idx: idx, id: id, live: true}, evicted, false, nil
}

// Free returns a pinned page to the freelist and drops it from the cache.
// It must be the only pin. Dirty bytes are discarded.
func (p *Pool) Free(pg *Page) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if pg == nil || pg.pool != p || !pg.live {
		return ErrUnpinned
	}
	fr := &p.frames[pg.idx]
	if fr.id != pg.id || !fr.used {
		return ErrUnpinned
	}
	if fr.pin != 1 {
		return gerr.Newf(gerr.InvalidArgument, "page %d has %d pins", pg.id, fr.pin)
	}
	if err := p.file.Free(pg.id); err != nil {
		return err
	}
	p.dirtyRemove(pg.idx)
	delete(p.index, pg.id)
	fr.used = false
	fr.pin = 0
	fr.buf = nil
	fr.id = 0
	p.free = append(p.free, pg.idx)
	pg.live = false
	return nil
}

// FlushAll writes every dirty frame and syncs the file.
func (p *Pool) FlushAll() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	for idx := p.dirtyHead; idx >= 0; {
		next := p.frames[idx].dirtyNext
		if err := p.flushFrame(idx); err != nil {
			return err
		}
		idx = next
	}
	return p.file.Sync()
}

// Close writes dirty frames and rejects later use. The page file stays open.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	for idx := p.dirtyHead; idx >= 0; {
		next := p.frames[idx].dirtyNext
		if err := p.flushFrame(idx); err != nil {
			return err
		}
		idx = next
	}
	if err := p.file.Sync(); err != nil {
		return err
	}
	p.closed = true
	return nil
}

// ID is the page this handle pins.
func (pg *Page) ID() PageID { return pg.id }

// Data returns the payload. The slice aliases the frame until Unpin.
func (pg *Page) Data() ([]byte, error) {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	fr, err := pg.frame()
	if err != nil {
		return nil, err
	}
	raw := payload(fr.buf)
	return raw[:len(raw):len(raw)], nil
}

// Type is the page kind stored in the frame header.
func (pg *Page) Type() (PageType, error) {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	fr, err := pg.frame()
	if err != nil {
		return 0, err
	}
	return pageTypeOf(fr.buf), nil
}

// SetType records the page kind and marks the page dirty.
func (pg *Page) SetType(typ PageType) error {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	fr, err := pg.frame()
	if err != nil {
		return err
	}
	setPageType(fr.buf, typ)
	pg.pool.dirtyAdd(pg.idx)
	return nil
}

// LSN is the page LSN stored in the frame. Phase 2E publishes it through the WAL.
func (pg *Page) LSN() (uint64, error) {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	fr, err := pg.frame()
	if err != nil {
		return 0, err
	}
	return pageLSNOf(fr.buf), nil
}

// SetLSN records lsn and marks the page dirty.
func (pg *Page) SetLSN(lsn uint64) error {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	fr, err := pg.frame()
	if err != nil {
		return err
	}
	setPageLSN(fr.buf, lsn)
	pg.pool.dirtyAdd(pg.idx)
	return nil
}

// MarkDirty includes the page in the next flush or eviction write-back.
func (pg *Page) MarkDirty() error {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	if _, err := pg.frame(); err != nil {
		return err
	}
	pg.pool.dirtyAdd(pg.idx)
	return nil
}

// Unpin releases one pin. The last pin puts the frame at the most-recent end
// of the eviction order.
func (pg *Page) Unpin() error {
	pg.pool.mu.Lock()
	defer pg.pool.mu.Unlock()
	if pg.pool.closed && !pg.live {
		return nil
	}
	fr, err := pg.frame()
	if err != nil {
		return err
	}
	fr.pin--
	if fr.pin == 0 {
		pg.pool.lruPushMRU(pg.idx)
	}
	pg.live = false
	return nil
}

func (pg *Page) frame() (*frame, error) {
	if pg == nil || pg.pool == nil || !pg.live {
		return nil, ErrUnpinned
	}
	fr := &pg.pool.frames[pg.idx]
	if !fr.used || fr.id != pg.id || fr.pin < 1 {
		return nil, ErrUnpinned
	}
	return fr, nil
}

func (p *Pool) pinFrame(idx int) {
	fr := &p.frames[idx]
	if fr.pin == 0 {
		p.lruRemove(idx)
	}
	fr.pin++
}

func (p *Pool) install(idx int, id PageID, img []byte) {
	fr := &p.frames[idx]
	if cap(fr.buf) < len(img) {
		fr.buf = make([]byte, len(img))
	}
	fr.buf = fr.buf[:len(img)]
	copy(fr.buf, img)
	fr.id = id
	fr.pin = 1
	fr.used = true
	fr.dirty = false
	fr.dirtyPrev = -1
	fr.dirtyNext = -1
	p.index[id] = idx
}

func (p *Pool) acquireFrame() (int, PageID, error) {
	if n := len(p.free); n > 0 {
		idx := p.free[n-1]
		p.free = p.free[:n-1]
		return idx, 0, nil
	}
	if p.lruHead < 0 {
		return 0, 0, gerr.New(gerr.ResourceExhausted, "buffer pool has no unpinned frame")
	}
	idx := p.lruHead
	p.lruRemove(idx)
	fr := &p.frames[idx]
	evicted := fr.id
	if fr.dirty {
		if err := p.flushFrame(idx); err != nil {
			p.lruPushLRU(idx)
			return 0, 0, err
		}
	}
	delete(p.index, fr.id)
	fr.used = false
	fr.id = 0
	p.evictions++
	return idx, evicted, nil
}

func (p *Pool) flushFrame(idx int) error {
	fr := &p.frames[idx]
	seal(fr.buf)
	if err := p.file.writeImage(fr.buf); err != nil {
		return err
	}
	p.dirtyRemove(idx)
	p.flushes++
	return nil
}

func (p *Pool) dirtyAdd(idx int) {
	fr := &p.frames[idx]
	if fr.dirty {
		return
	}
	fr.dirty = true
	fr.dirtyPrev = -1
	fr.dirtyNext = p.dirtyHead
	if p.dirtyHead >= 0 {
		p.frames[p.dirtyHead].dirtyPrev = idx
	}
	p.dirtyHead = idx
}

func (p *Pool) dirtyRemove(idx int) {
	fr := &p.frames[idx]
	if !fr.dirty {
		return
	}
	if fr.dirtyPrev >= 0 {
		p.frames[fr.dirtyPrev].dirtyNext = fr.dirtyNext
	} else {
		p.dirtyHead = fr.dirtyNext
	}
	if fr.dirtyNext >= 0 {
		p.frames[fr.dirtyNext].dirtyPrev = fr.dirtyPrev
	}
	fr.dirty = false
	fr.dirtyPrev = -1
	fr.dirtyNext = -1
}

func (p *Pool) lruPushMRU(idx int) {
	fr := &p.frames[idx]
	fr.lruPrev = p.lruTail
	fr.lruNext = -1
	if p.lruTail >= 0 {
		p.frames[p.lruTail].lruNext = idx
	} else {
		p.lruHead = idx
	}
	p.lruTail = idx
}

func (p *Pool) lruPushLRU(idx int) {
	fr := &p.frames[idx]
	fr.lruNext = p.lruHead
	fr.lruPrev = -1
	if p.lruHead >= 0 {
		p.frames[p.lruHead].lruPrev = idx
	} else {
		p.lruTail = idx
	}
	p.lruHead = idx
}

func (p *Pool) lruRemove(idx int) {
	fr := &p.frames[idx]
	if fr.lruPrev < 0 && fr.lruNext < 0 && p.lruHead != idx {
		return
	}
	if fr.lruPrev >= 0 {
		p.frames[fr.lruPrev].lruNext = fr.lruNext
	} else {
		p.lruHead = fr.lruNext
	}
	if fr.lruNext >= 0 {
		p.frames[fr.lruNext].lruPrev = fr.lruPrev
	} else {
		p.lruTail = fr.lruPrev
	}
	fr.lruPrev = -1
	fr.lruNext = -1
}

func fire(h PoolHooks, id, evicted PageID, hit bool, err error) {
	if evicted != 0 && h.Evict != nil {
		h.Evict(evicted)
	}
	if err != nil {
		return
	}
	if hit {
		if h.Hit != nil {
			h.Hit(id)
		}
		return
	}
	if h.Miss != nil {
		h.Miss(id)
	}
}

package disk

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestPoolHitMissAndHooks(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hits, misses atomic.Int32
	pool.SetHooks(PoolHooks{
		Hit:  func(PageID) { hits.Add(1) },
		Miss: func(PageID) { misses.Add(1) },
	})
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pool.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Unpin(); err != nil {
		t.Fatal(err)
	}
	pg, err = pool.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Unpin(); err != nil {
		t.Fatal(err)
	}
	stats := pool.Stats()
	if stats.Hits != 1 || stats.Misses != 1 || stats.HitRatio() != 0.5 {
		t.Fatalf("stats %+v ratio %v", stats, stats.HitRatio())
	}
	if hits.Load() != 1 || misses.Load() != 1 {
		t.Fatalf("hooks hit %d miss %d", hits.Load(), misses.Load())
	}
	if _, err := pool.Get(0); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("header: %v", err)
	}
	if _, err := NewPool(nil, 1); err == nil {
		t.Fatal("nil file")
	}
	if _, err := NewPool(pf, 0); err == nil {
		t.Fatal("zero frames")
	}
}

func TestPoolEvictsLeastRecentlyUnpinned(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 2)
	if err != nil {
		t.Fatal(err)
	}
	var evicted atomic.Uint64
	pool.SetHooks(PoolHooks{
		Evict: func(id PageID) { evicted.Store(uint64(id)) },
	})
	first, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	data, err := first.Data()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0x11
	if err := first.MarkDirty(); err != nil || first.Unpin() != nil {
		t.Fatal("dirty unpin")
	}
	second, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	data, err = second.Data()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0x22
	if err := second.MarkDirty(); err != nil || second.Unpin() != nil {
		t.Fatal("dirty unpin")
	}
	third, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if evicted.Load() != uint64(first.ID()) {
		t.Fatalf("evicted %d, want %d", evicted.Load(), first.ID())
	}
	info, err := pf.ReadPage(first.ID())
	if err != nil || info.Data[0] != 0x11 {
		t.Fatalf("write-back %v %v", info.Data, err)
	}
	pinned, err := pool.Get(second.ID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Get(first.ID())
	if !gerr.IsCode(err, gerr.ResourceExhausted) || !gerr.Retryable(err) {
		t.Fatalf("expected retryable exhaustion, got %v", err)
	}
	if err := third.Unpin(); err != nil || pinned.Unpin() != nil {
		t.Fatal(err)
	}
	stats := pool.Stats()
	if stats.Evictions != 1 || stats.Flushes < 1 || stats.Pins != 0 {
		t.Fatalf("stats %+v", stats)
	}
}

func TestPoolFlushAllAndClose(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 2)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	data, err := pg.Data()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0xab
	if err := pg.SetLSN(7); err != nil {
		t.Fatal(err)
	}
	if err := pool.FlushAll(); err != nil {
		t.Fatal(err)
	}
	info, err := pf.ReadPage(pg.ID())
	if err != nil || info.Data[0] != 0xab || info.LSN != 7 {
		t.Fatalf("flush %+v %v", info, err)
	}
	if pool.Stats().Dirty != 0 {
		t.Fatalf("still dirty %+v", pool.Stats())
	}
	data[1] = 0xcd
	if err := pg.MarkDirty(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	info, err = pf.ReadPage(pg.ID())
	if err != nil || info.Data[1] != 0xcd {
		t.Fatalf("close flush %+v %v", info, err)
	}
	if _, err := pool.Get(pg.ID()); err != ErrClosed {
		t.Fatalf("get after close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolFreeReturnsPage(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 1)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	id := pg.ID()
	other, err := pool.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Free(pg); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("free with two pins: %v", err)
	}
	if err := other.Unpin(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Free(pg); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Data(); err != ErrUnpinned {
		t.Fatalf("data: %v", err)
	}
	if _, err := pool.Get(id); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("get freed: %v", err)
	}
	reused, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID() != id {
		t.Fatalf("reused %d, want %d", reused.ID(), id)
	}
}

func TestPoolPinnedPageStays(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pool.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := pg.Data()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 9
	if err := pg.MarkDirty(); err != nil {
		t.Fatal(err)
	}
	other, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Get(other); !gerr.IsCode(err, gerr.ResourceExhausted) {
		t.Fatalf("%v", err)
	}
	if data[0] != 9 {
		t.Fatalf("pinned byte %d", data[0])
	}
	if err := pg.Unpin(); err != nil {
		t.Fatal(err)
	}
	if err := pg.Unpin(); err != ErrUnpinned {
		t.Fatalf("second unpin %v", err)
	}
}

func TestPoolConcurrentReaders(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 2)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]PageID, 4)
	for i := range ids {
		id, err := pf.Allocate()
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				var pg *Page
				var err error
				for attempt := 0; attempt < 100; attempt++ {
					pg, err = pool.Get(ids[n%len(ids)])
					if err == nil || !gerr.IsCode(err, gerr.ResourceExhausted) {
						break
					}
				}
				if err != nil {
					t.Errorf("get: %v", err)
					return
				}
				if _, err := pg.Data(); err != nil {
					t.Errorf("data: %v", err)
					return
				}
				if err := pg.Unpin(); err != nil {
					t.Errorf("unpin: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

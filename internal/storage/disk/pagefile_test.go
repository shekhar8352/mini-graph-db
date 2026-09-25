package disk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk/fs"
)

func createFile(t *testing.T, pageSize int) (*PageFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "heap.db")
	pf, err := Create(path, Options{PageSize: pageSize})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = pf.Close() })
	return pf, path
}

func TestCreateOpenRoundTrip(t *testing.T) {
	created := time.Date(2026, 9, 25, 12, 0, 0, 123, time.UTC)
	var id [16]byte
	for i := range id {
		id[i] = byte(i + 1)
	}
	path := filepath.Join(t.TempDir(), "heap.db")
	pf, err := Create(path, Options{PageSize: 4096, UUID: id, Created: created})
	if err != nil {
		t.Fatal(err)
	}
	meta := pf.Meta()
	if meta.Version != FormatVersion || meta.PageSize != 4096 || meta.UUID != id {
		t.Fatalf("meta %+v", meta)
	}
	if !meta.Created.Equal(created) {
		t.Fatalf("created %s", meta.Created)
	}
	if meta.PageCount != 1 || meta.FreePages != 0 || meta.CheckpointLSN != 0 || meta.FreelistHead != 0 {
		t.Fatalf("meta %+v", meta)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	again := opened.Meta()
	if again.UUID != id || again.PageSize != 4096 || !again.Created.Equal(created) {
		t.Fatalf("reopen meta %+v", again)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4096 {
		t.Fatalf("file size %d", info.Size())
	}
}

func TestDefaultPageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.db")
	pf, err := Create(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pf.Close() }()
	if pf.PageSize() != DefaultPageSize || pf.PayloadSize() != DefaultPageSize-pageOverhead {
		t.Fatalf("page size %d payload %d", pf.PageSize(), pf.PayloadSize())
	}
}

func TestRejectsBadPageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.db")
	for _, size := range []int{0, 1024, 6000, 1 << 20} {
		if size == 0 {
			continue
		}
		_, err := Create(path, Options{PageSize: size})
		if !gerr.IsCode(err, gerr.InvalidArgument) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
}

func TestCreateExistingAndOpenMissing(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	if err := pf.Sync(); err != nil {
		t.Fatal(err)
	}
	_, err := Create(path, Options{PageSize: MinPageSize})
	if !gerr.IsCode(err, gerr.AlreadyExists) {
		t.Fatalf("create existing: %v", err)
	}
	_, err = Open(filepath.Join(t.TempDir(), "missing.db"), Options{})
	if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("open missing: %v", err)
	}
}

func TestOpenRejectsNewerVersion(t *testing.T) {
	_, path := createFile(t, MinPageSize)
	// Close through the cleanup at the end; patch after an explicit close.
	// createFile's cleanup closes again, which is idempotent.
	raw := readHeap(t, path)
	binary.LittleEndian.PutUint16(raw[pageHeaderSize+offVersion:], FormatVersion+1)
	seal(raw[:MinPageSize])
	writeHeap(t, path, raw)

	_, err := Open(path, Options{})
	if !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("err %v", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("newer")) {
		t.Fatalf("message %v", err)
	}
}

func TestOpenRejectsBadMagicAndChecksum(t *testing.T) {
	_, path := createFile(t, MinPageSize)
	raw := readHeap(t, path)
	raw[pageHeaderSize] = 'X'
	writeHeap(t, path, raw)
	_, err := Open(path, Options{})
	if !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("magic: %v", err)
	}

	pf, path := createFile(t, MinPageSize)
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	raw = readHeap(t, path)
	raw[pageHeaderSize+offUUID] ^= 0xff
	writeHeap(t, path, raw)
	_, err = Open(path, Options{})
	if !gerr.IsCode(err, gerr.Corruption) {
		t.Fatalf("checksum: %v", err)
	}
}

func TestOpenRejectsShortFileAndIgnoresTornTail(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	if _, err := pf.Allocate(); err != nil {
		t.Fatal(err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	raw := readHeap(t, path)
	torn := append(append([]byte{}, raw...), bytes.Repeat([]byte{0xab}, 100)...)
	writeHeap(t, path, torn)
	opened, err := Open(path, Options{PageSize: MinPageSize})
	if err != nil {
		t.Fatal(err)
	}
	if opened.PageCount() != 2 {
		t.Fatalf("page count %d", opened.PageCount())
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Truncate(path, 10); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path, Options{})
	if !gerr.IsCode(err, gerr.Corruption) && !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("short file: %v", err)
	}
}

func TestOpenRejectsMismatchedPageSize(t *testing.T) {
	_, path := createFile(t, MinPageSize)
	_, err := Open(path, Options{PageSize: DefaultPageSize})
	if !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestWriteReadAndPageLSN(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	pool, err := NewPool(pf, 1)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pool.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	id := pg.ID()
	if err := pg.SetLSN(99); err != nil {
		t.Fatal(err)
	}
	data, err := pg.Data()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 1
	if err := pg.MarkDirty(); err != nil {
		t.Fatal(err)
	}
	if err := pg.Unpin(); err != nil {
		t.Fatal(err)
	}
	if err := pool.FlushAll(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte{0x2a}, pf.PayloadSize())
	if err := pf.WritePage(id, payload); err != nil {
		t.Fatal(err)
	}
	info, err := pf.ReadPage(id)
	if err != nil {
		t.Fatal(err)
	}
	if info.LSN != 99 || info.Type != TypeData || !bytes.Equal(info.Data, payload) {
		t.Fatalf("page %+v", info)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	info, err = opened.ReadPage(id)
	if err != nil {
		t.Fatal(err)
	}
	if info.LSN != 99 || info.Data[0] != 0x2a {
		t.Fatalf("reopen %+v", info)
	}
}

func TestWritePageRejects(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.WritePage(0, make([]byte, pf.PayloadSize())); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("header: %v", err)
	}
	if err := pf.WritePage(id, []byte{1}); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("length: %v", err)
	}
	if err := pf.Free(id); err != nil {
		t.Fatal(err)
	}
	if err := pf.WritePage(id, make([]byte, pf.PayloadSize())); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("free: %v", err)
	}
	_, err = pf.ReadPage(99)
	if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestFreelistLIFOAndReopen(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	first, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	third, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("ids %d %d %d", first, second, third)
	}
	if err := pf.Free(second); err != nil {
		t.Fatal(err)
	}
	reused, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if reused != second {
		t.Fatalf("reused %d", reused)
	}
	if err := pf.Free(third); err != nil || pf.Free(first) != nil {
		t.Fatal("free")
	}
	if pf.Meta().FreePages != 2 || pf.PageCount() != 4 {
		t.Fatalf("meta %+v", pf.Meta())
	}
	if err := pf.Free(first); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("double free: %v", err)
	}
	if err := pf.Free(0); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("free header: %v", err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	if opened.Meta().FreePages != 2 {
		t.Fatalf("reopen meta %+v", opened.Meta())
	}
	got, err := opened.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if got != first && got != third {
		t.Fatalf("allocate after reopen %d", got)
	}
}

func TestTruncateDropsFreeTail(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	keep, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	drop1, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	drop2, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, pf.PayloadSize())
	payload[0] = 7
	if err := pf.WritePage(keep, payload); err != nil {
		t.Fatal(err)
	}
	if err := pf.SetRoot(storage.KSNode, keep); err != nil {
		t.Fatal(err)
	}
	if err := pf.Free(drop2); err != nil || pf.Free(drop1) != nil {
		t.Fatal("free")
	}
	if err := pf.Truncate(1); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("truncate root: %v", err)
	}
	if err := pf.Truncate(uint64(drop1)); err != nil {
		t.Fatal(err)
	}
	if pf.PageCount() != uint64(drop1) || pf.Meta().FreePages != 0 {
		t.Fatalf("meta %+v", pf.Meta())
	}
	info, err := pf.ReadPage(keep)
	if err != nil || info.Data[0] != 7 {
		t.Fatalf("kept page %v %v", info, err)
	}
	next, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if next != drop1 {
		t.Fatalf("grew at %d, want %d", next, drop1)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	root, err := opened.Root(storage.KSNode)
	if err != nil || root != keep {
		t.Fatalf("root %d %v", root, err)
	}
}

func TestTruncateRejectsLivePage(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	if _, err := pf.Allocate(); err != nil {
		t.Fatal(err)
	}
	live, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Truncate(uint64(live)); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if _, err := pf.ReadPage(live); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderFieldsPersist(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.SetCheckpointLSN(42); err != nil {
		t.Fatal(err)
	}
	if err := pf.SetRoot(storage.KSEdge, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.Root('Z'); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("keyspace: %v", err)
	}
	if err := pf.Free(id); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("free root: %v", err)
	}
	free, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Free(free); err != nil {
		t.Fatal(err)
	}
	if err := pf.SetRoot(storage.KSCatalog, free); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("root free: %v", err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	if opened.CheckpointLSN() != 42 {
		t.Fatalf("lsn %d", opened.CheckpointLSN())
	}
	root, err := opened.Root(storage.KSEdge)
	if err != nil || root != id {
		t.Fatalf("root %d %v", root, err)
	}
	for _, ks := range rootOrder {
		if ks == storage.KSEdge {
			continue
		}
		got, err := opened.Root(ks)
		if err != nil || got != 0 {
			t.Fatalf("keyspace %q root %d %v", ks, got, err)
		}
	}
}

func TestPageIDMismatchIsCorruption(t *testing.T) {
	pf, path := createFile(t, MinPageSize)
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	raw := readHeap(t, path)
	off := int(id) * MinPageSize
	binary.LittleEndian.PutUint64(raw[off:], uint64(id+5))
	seal(raw[off : off+MinPageSize])
	writeHeap(t, path, raw)
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	_, err = opened.ReadPage(id)
	if !gerr.IsCode(err, gerr.Corruption) {
		t.Fatalf("%v", err)
	}
}

func TestConcurrentAllocate(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	var mu sync.Mutex
	seen := map[PageID]struct{}{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 40; n++ {
				id, err := pf.Allocate()
				if err != nil {
					t.Errorf("alloc: %v", err)
					return
				}
				mu.Lock()
				if _, ok := seen[id]; ok {
					t.Errorf("duplicate page %d", id)
				}
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 8*40 {
		t.Fatalf("pages %d", len(seen))
	}
}

func TestFaultShortWriteAndReorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.db")
	raw, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fault := fs.Wrap(fs.FromOS(raw))
	pf, err := Create(path, Options{PageSize: MinPageSize, File: fault})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pf.Close() })
	id, err := pf.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0x5a}, pf.PayloadSize())
	fault.SetReorder(true)
	if err := pf.WritePage(id, payload); err != nil {
		t.Fatal(err)
	}
	if fault.Pending() == 0 {
		t.Fatal("write was not queued")
	}
	info, err := pf.ReadPage(id)
	if err != nil || info.Data[0] != 0x5a {
		t.Fatalf("queued read %v %v", info.Data, err)
	}
	fault.Discard()
	info, err = pf.ReadPage(id)
	if err != nil || info.Data[0] != 0 {
		t.Fatalf("discarded read %v %v", info.Data, err)
	}
	if err := pf.WritePage(id, payload); err != nil {
		t.Fatal(err)
	}
	if err := pf.Sync(); err != nil {
		t.Fatal(err)
	}
	if fault.Pending() != 0 {
		t.Fatalf("pending %d", fault.Pending())
	}
	info, err = pf.ReadPage(id)
	if err != nil || info.Data[0] != 0x5a {
		t.Fatalf("synced read %v %v", info.Data, err)
	}

	fault.SetReorder(false)
	// Eight bytes only covers the page id, which this write does not change.
	// A longer short write tears the payload and leaves the old checksum.
	fault.ShortWrite(64)
	if err := pf.WritePage(id, bytes.Repeat([]byte{0x11}, pf.PayloadSize())); err == nil || !gerr.IsCode(err, gerr.Unavailable) {
		t.Fatalf("short write: %v", err)
	}
	_, err = pf.ReadPage(id)
	if !gerr.IsCode(err, gerr.Corruption) {
		t.Fatalf("torn page: %v", err)
	}

	fault.FailNextSync(errors.New("sync failed"))
	if err := pf.Sync(); err == nil {
		t.Fatal("expected sync failure")
	}
}

func TestClosedPageFile(t *testing.T) {
	pf, _ := createFile(t, MinPageSize)
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.Allocate(); !errors.Is(err, ErrClosed) {
		t.Fatalf("%v", err)
	}
}

func readHeap(t *testing.T, path string) []byte {
	t.Helper()
	// The page file may still be open. Read through the filesystem.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeHeap(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

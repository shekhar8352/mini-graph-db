package disk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

func seqKey(i int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(i))
	return b[:]
}

func countWAL(t *testing.T, dir string) int {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, walDirName, "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

// TestAbandonBetweenCommits crashes after every acknowledged commit and
// reopens. Each commit's pages may be unpublished only if Commit failed;
// a nil Commit is recovered.
func TestAbandonBetweenCommits(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	for i := 0; i < n; i++ {
		eng := openEngine(t, dir)
		tx, err := eng.Begin(storage.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(storage.KSNode, seqKey(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		eng.abandon()

		eng = openEngine(t, dir)
		tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j <= i; j++ {
			got, err := tx.Get(storage.KSNode, seqKey(j))
			if err != nil || len(got) != 1 || got[0] != byte(j) {
				t.Fatalf("after %d, key %d: %q %v", i, j, got, err)
			}
		}
		if _, err := tx.Get(storage.KSNode, seqKey(i+1)); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("future key: %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := eng.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCrashCuts truncates the only copy of acknowledged commits (the WAL)
// at 1000 offsets and flips bytes in complete records. A torn tail recovers
// a prefix of those commits. A bad checksum is Corruption.
func TestCrashCuts(t *testing.T) {
	dir := t.TempDir()
	eng, err := OpenEngine(dir, EngineOptions{
		PageSize:           MinPageSize,
		CheckpointInterval: -1,
		CheckpointBytes:    -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := os.ReadFile(filepath.Join(dir, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	const nKeys = 8
	for i := 0; i < nKeys; i++ {
		tx, err := eng.Begin(storage.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(storage.KSNode, seqKey(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	eng.abandon()

	segs, err := filepath.Glob(filepath.Join(dir, walDirName, "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments %d", len(segs))
	}
	walBytes, err := os.ReadFile(segs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(walBytes) == 0 {
		t.Fatal("empty wal")
	}

	restore := func(wal []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, dbFileName), empty, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(segs[0], wal, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(off int, flipped bool) {
		t.Helper()
		eng, err := OpenEngine(dir, testOpts())
		if err != nil {
			var ge *gerr.Error
			if errors.As(err, &ge) && ge.Code() == gerr.Corruption {
				return
			}
			t.Fatalf("off %d flipped %v: %v", off, flipped, err)
		}
		defer eng.abandon()
		tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for i := 0; i < nKeys; i++ {
			got, err := tx.Get(storage.KSNode, seqKey(i))
			if errors.Is(err, storage.ErrNotFound) {
				for j := i + 1; j < nKeys; j++ {
					if _, jerr := tx.Get(storage.KSNode, seqKey(j)); !errors.Is(jerr, storage.ErrNotFound) {
						t.Fatalf("off %d gap at %d then %d: %v", off, i, j, jerr)
					}
				}
				break
			}
			if err != nil || !bytes.Equal(got, []byte{byte(i)}) {
				t.Fatalf("off %d key %d: %q %v", off, i, got, err)
			}
			seen++
		}
		if !flipped && off == 0 && seen != 0 {
			t.Fatalf("empty wal recovered %d keys", seen)
		}
		if !flipped && off == len(walBytes) && seen != nKeys {
			t.Fatalf("full wal recovered %d keys", seen)
		}
	}

	rng := rand.New(rand.NewSource(20261002))
	cuts := make([]int, 0, 1000)
	cuts = append(cuts, 0, len(walBytes))
	for len(cuts) < 1000 {
		cuts = append(cuts, rng.Intn(len(walBytes)+1))
	}
	for _, off := range cuts {
		restore(walBytes[:off])
		check(off, false)
	}
	for i := 0; i < 64; i++ {
		off := rng.Intn(len(walBytes))
		flipped := bytes.Clone(walBytes)
		flipped[off] ^= 0xff
		restore(flipped)
		check(off, true)
	}
}

func TestCheckpointerRuns(t *testing.T) {
	dir := t.TempDir()
	eng, err := OpenEngine(dir, EngineOptions{
		CheckpointInterval: -1,
		CheckpointBytes:    1,
		WAL:                wal.Options{SegmentSize: 4096},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	peak := countWAL(t, dir)
	for i := 0; i < 30; i++ {
		tx, err := eng.Begin(storage.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(storage.KSNode, seqKey(i), bytes.Repeat([]byte{byte(i)}, 200)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if n := countWAL(t, dir); n > peak {
			peak = n
		}
	}
	if peak < 2 {
		t.Fatalf("peak segments %d", peak)
	}
	deadline := time.Now().Add(3 * time.Second)
	for countWAL(t, dir) >= peak {
		if time.Now().After(deadline) {
			t.Fatalf("checkpointer left %d segments, peak %d", countWAL(t, dir), peak)
		}
		time.Sleep(20 * time.Millisecond)
	}
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		got, err := tx.Get(storage.KSNode, seqKey(i))
		if err != nil || len(got) != 200 {
			t.Fatalf("key %d: %d %v", i, len(got), err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

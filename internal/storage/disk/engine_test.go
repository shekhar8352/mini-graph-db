package disk

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/enginetest"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

func testOpts() EngineOptions {
	return EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1}
}

func openEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	eng, err := OpenEngine(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestConformance(t *testing.T) {
	enginetest.Run(t, func() storage.Engine {
		return openEngine(t, "")
	})
}

func TestReopenAndOverflow(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0xab}, 3000)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSEdge, []byte("big"), big); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}

	eng = openEngine(t, dir)
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tx.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("reopen a: %q %v", got, err)
	}
	got, err = tx.Get(storage.KSEdge, []byte("big"))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("overflow len %d err %v", len(got), err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverWithoutPublish(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("kept"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	eng.stopAfterSync = true
	if err := tx.Commit(); !errors.Is(err, errStopAfterSync) {
		t.Fatalf("commit: %v", err)
	}
	eng.abandon()

	eng = openEngine(t, dir)
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tx.Get(storage.KSNode, []byte("kept"))
	if err != nil || string(got) != "yes" {
		t.Fatalf("recovered: %q %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUncommittedNotRecovered(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("kept"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("lost"), []byte("no")); err != nil {
		t.Fatal(err)
	}
	eng.abandon()

	eng = openEngine(t, dir)
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Get(storage.KSNode, []byte("lost")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("lost key: %v", err)
	}
	got, err := tx.Get(storage.KSNode, []byte("kept"))
	if err != nil || string(got) != "yes" {
		t.Fatalf("kept: %q %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnly(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSCatalog, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenEngine(dir, EngineOptions{ReadOnly: true, CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Begin(storage.TxOptions{}); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatalf("write begin: %v", err)
	}
	tx, err = ro.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tx.Get(storage.KSCatalog, []byte("k"))
	if err != nil || string(got) != "v" {
		t.Fatalf("read: %q %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenEngine(t.TempDir(), EngineOptions{ReadOnly: true}); !errors.As(err, new(*gerr.Error)) {
		t.Fatalf("missing dir: %v", err)
	}
}

func TestNewerFormatRefused(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, dbFileName)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{2, 0}, int64(pageHeaderSize+offVersion)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = OpenEngine(dir, testOpts())
	var ge *gerr.Error
	if !errors.As(err, &ge) || ge.Code() != gerr.InvalidArgument {
		t.Fatalf("newer format: %v", err)
	}
}

func TestCorruptPage(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, dbFileName)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a payload byte of page 1. The header stays valid.
	if _, err := f.WriteAt([]byte{0xff}, int64(DefaultPageSize+40)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	eng, err = OpenEngine(dir, testOpts())
	if err != nil {
		var ge *gerr.Error
		if errors.As(err, &ge) && ge.Code() == gerr.Corruption {
			return
		}
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Get(storage.KSNode, []byte("a"))
	var ge *gerr.Error
	if !errors.As(err, &ge) || ge.Code() != gerr.Corruption {
		t.Fatalf("get corrupt page: %v", err)
	}
}

func TestCheckpointTruncates(t *testing.T) {
	dir := t.TempDir()
	eng, err := OpenEngine(dir, EngineOptions{
		CheckpointInterval: -1,
		CheckpointBytes:    -1,
		WAL:                wal.Options{SegmentSize: 4096},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		tx, err := eng.Begin(storage.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		key := []byte{byte(i)}
		if err := tx.Put(storage.KSNode, key, bytes.Repeat([]byte{byte(i)}, 200)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	before, err := filepath.Glob(filepath.Join(dir, walDirName, "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 2 {
		t.Fatalf("segments before checkpoint: %d", len(before))
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	after, err := filepath.Glob(filepath.Join(dir, walDirName, "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) {
		t.Fatalf("segments %d, was %d", len(after), len(before))
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	eng = openEngine(t, dir)
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		got, err := tx.Get(storage.KSNode, []byte{byte(i)})
		if err != nil || len(got) != 200 {
			t.Fatalf("key %d: %d %v", i, len(got), err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

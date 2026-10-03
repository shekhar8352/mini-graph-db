package txn

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
)

func TestDiskReopenAndUncommitted(t *testing.T) {
	dir := t.TempDir()
	eng, err := disk.OpenEngine(dir, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(eng, Options{})
	if err != nil {
		t.Fatal(err)
	}
	putKey(t, m, "kept", "yes")
	tx := begin(t, m, false)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("c"), []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	lost := begin(t, m, false)
	if err := lost.Put(storage.KSNode, []byte("lost"), []byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	eng, err = disk.OpenEngine(dir, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	m, err = Open(eng, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for _, key := range []string{"kept", "a", "b", "c"} {
		if string(getKey(t, m, key)) == "" {
			t.Fatalf("missing %s", key)
		}
	}
	tx = begin(t, m, true)
	if _, err := tx.Get(storage.KSNode, []byte("lost")); err == nil {
		t.Fatal("uncommitted key was recovered")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if m.MVCCStats().CommitTS == 0 {
		t.Fatal("oracle did not survive reopen")
	}
}

package disk

import (
	"errors"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/txn"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

func TestMVCCCommitIsAtomicOnRecover(t *testing.T) {
	dir := t.TempDir()
	eng := openEngine(t, dir)
	m, err := txn.Open(eng, txn.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("kept"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b", "c"} {
		if err := tx.Put(storage.KSNode, []byte(key), []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	eng.stopAfterSync = true
	if err := tx.Commit(); !errors.Is(err, errStopAfterSync) {
		t.Fatalf("commit: %v", err)
	}
	eng.abandon()
	_ = m.Close()

	eng = openEngine(t, dir)
	defer func() { _ = eng.Close() }()
	m, err = txn.Open(eng, txn.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	view, err := m.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kept", "a", "b", "c"} {
		got, err := view.Get(storage.KSNode, []byte(key))
		if err != nil || string(got) != valueOf(key) {
			t.Fatalf("recovered %s: %q %v", key, got, err)
		}
	}
	if _, err := view.Get(storage.KSNode, []byte("partial")); err == nil {
		t.Fatal("unexpected key")
	}
	if err := view.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err = m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("z"), []byte("z")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Sync(); err != nil {
		t.Fatal(err)
	}
	r, err := eng.wal.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	abort := false
	for {
		rec, err := r.Next()
		if err != nil {
			break
		}
		if rec.Type == wal.TypeTxnAbort {
			abort = true
		}
	}
	if !abort {
		t.Fatal("rollback did not append TxnAbort")
	}
}

func valueOf(key string) string {
	if key == "kept" {
		return "yes"
	}
	return key
}

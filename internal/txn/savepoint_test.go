package txn

import (
	"errors"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
)

func TestSavepoint(t *testing.T) {
	m, err := Open(memory.Open(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	tx, err := m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sp := tx.(*Tx)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	mark, err := sp.Mark()
	if err != nil || mark != 1 {
		t.Fatalf("mark %d %v", mark, err)
	}
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("b"), []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := sp.RollbackTo(mark); err != nil {
		t.Fatal(err)
	}
	got, err := tx.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("a %q %v", got, err)
	}
	if _, err := tx.Get(storage.KSNode, []byte("b")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("b %v", err)
	}
	if err := sp.RollbackTo(mark); gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatalf("stale mark %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = m.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err = tx.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("committed %q %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

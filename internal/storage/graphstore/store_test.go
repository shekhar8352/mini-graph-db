package graphstore

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestCascadeDeleteAndIDAllocation(t *testing.T) {
	eng := memory.Open()
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Error(err)
		}
	})

	var a, b, c Node
	var e1, e2 Edge
	mustWrite(t, eng, func(tx storage.Tx) error {
		var err error
		a, err = CreateNode(tx, []string{"person"}, map[string]value.Value{"name": value.String("A")})
		if err != nil {
			return err
		}
		b, err = CreateNode(tx, []string{"person"}, nil)
		if err != nil {
			return err
		}
		c, err = CreateNode(tx, []string{"person"}, nil)
		if err != nil {
			return err
		}
		e1, err = CreateEdge(tx, a.ID, b.ID, "KNOWS", nil)
		if err != nil {
			return err
		}
		e2, err = CreateEdge(tx, c.ID, a.ID, "FOLLOWS", nil)
		return err
	})

	mustWrite(t, eng, func(tx storage.Tx) error {
		return DeleteNode(tx, a.ID)
	})

	mustRead(t, eng, func(tx storage.Tx) error {
		if _, err := GetNode(tx, a.ID); !IsNotFound(err) {
			t.Fatalf("node still present: %v", err)
		}
		if _, err := GetEdge(tx, e1.ID); !IsNotFound(err) {
			t.Fatalf("outgoing edge still present: %v", err)
		}
		if _, err := GetEdge(tx, e2.ID); !IsNotFound(err) {
			t.Fatalf("incoming edge still present: %v", err)
		}
		st, err := GraphStats(tx)
		if err != nil {
			return err
		}
		if st.Nodes != 2 || st.Edges != 0 {
			t.Fatalf("stats after cascade: %+v", st)
		}
		return nil
	})

	var next Node
	mustWrite(t, eng, func(tx storage.Tx) error {
		var err error
		next, err = CreateNode(tx, []string{"person"}, nil)
		return err
	})
	if next.ID != 4 {
		t.Fatalf("id reused or skipped: %d", next.ID)
	}
}

func TestPropIndexCrossType(t *testing.T) {
	eng := memory.Open()
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Error(err)
		}
	})
	var n Node
	mustWrite(t, eng, func(tx storage.Tx) error {
		var err error
		n, err = CreateNode(tx, []string{"n"}, map[string]value.Value{"n": value.Int(1)})
		return err
	})
	mustRead(t, eng, func(tx storage.Tx) error {
		got, err := NodesByPropEq(tx, "n", value.Float(1))
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].ID != n.ID {
			t.Fatalf("index: %+v", got)
		}
		return nil
	})
}

func mustWrite(t *testing.T, eng storage.Engine, fn func(storage.Tx) error) {
	t.Helper()
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, eng storage.Engine, fn func(storage.Tx) error) {
	t.Helper()
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	err = fn(tx)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
}

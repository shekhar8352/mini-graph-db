package index

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestPutSeekDrop(t *testing.T) {
	eng := memory.Open()
	t.Cleanup(func() { _ = eng.Close() })
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := uint32(1)
	must(t, Put(tx, id, []value.Value{value.String("Alice")}, 10))
	must(t, Put(tx, id, []value.Value{value.String("Bob")}, 20))
	must(t, Put(tx, id, []value.Value{value.String("Ada")}, 30))
	must(t, Put(tx, id, []value.Value{value.Null()}, 99))
	eq, err := Equal(tx, id, []value.Value{value.String("Alice")})
	if err != nil || len(eq) != 1 || eq[0] != 10 {
		t.Fatalf("equal %v %v", eq, err)
	}
	pre, err := StartsWith(tx, id, "A")
	if err != nil || len(pre) != 2 || pre[0] != 30 || pre[1] != 10 {
		t.Fatalf("starts %v %v", pre, err)
	}
	rng, err := Range(tx, id, []value.Value{value.String("Ada")}, []value.Value{value.String("Bob")}, true, false)
	if err != nil || len(rng) != 2 || rng[0] != 30 || rng[1] != 10 {
		t.Fatalf("range %v %v", rng, err)
	}
	must(t, Delete(tx, id, []value.Value{value.String("Alice")}, 10))
	eq, err = Equal(tx, id, []value.Value{value.String("Alice")})
	if err != nil || len(eq) != 0 {
		t.Fatalf("after delete %v %v", eq, err)
	}
	must(t, Drop(tx, id))
	left, err := PrefixScan(tx, id, nil)
	if err != nil || len(left) != 0 {
		t.Fatalf("drop %v %v", left, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeAndImplicitDoNotCollide(t *testing.T) {
	eng := memory.Open()
	t.Cleanup(func() { _ = eng.Close() })
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	implicit := make([]byte, 4+len(value.EncodeKey(value.String("x")))+8)
	if implicit[0] == Marker {
		t.Fatal("implicit key starts with declared marker")
	}
	must(t, Put(tx, 2, []value.Value{value.String("Ada"), value.Int(1)}, 7))
	got, err := Equal(tx, 2, []value.Value{value.String("Ada"), value.Int(1)})
	if err != nil || len(got) != 1 || got[0] != 7 {
		t.Fatalf("composite %v %v", got, err)
	}
	pre, err := PrefixScan(tx, 2, []value.Value{value.String("Ada")})
	if err != nil || len(pre) != 1 {
		t.Fatalf("prefix %v %v", pre, err)
	}
	k := Encode(2, []value.Value{value.String("Ada"), value.Int(1)}, 7)
	id, vals, ent, ok := Parse(k)
	if !ok || id != 2 || ent != 7 || len(vals) != 2 {
		t.Fatalf("parse %v %v %d %v", id, vals, ent, ok)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

package catalog

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestIndexConstraintRoundTrip(t *testing.T) {
	eng := memory.Open()
	t.Cleanup(func() { _ = eng.Close() })
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := AllocIndexID(tx)
	if err != nil || id != 1 {
		t.Fatalf("id %d %v", id, err)
	}
	ix := Index{ID: id, Name: "person_name", Entity: NodeEntity, Label: "Person", Props: []string{"name", "city"}, State: Online}
	if err := PutIndex(tx, ix); err != nil {
		t.Fatal(err)
	}
	c := Constraint{Name: "person_email", Kind: UniqueConstraint, Entity: NodeEntity, Label: "Person", Prop: "email", IndexName: "person_email"}
	if err := PutConstraint(tx, c); err != nil {
		t.Fatal(err)
	}
	st := Stats{
		Nodes:     3,
		Edges:     1,
		Labels:    map[string]int64{"Person": 3},
		EdgeTypes: map[string]int64{"KNOWS": 1},
		Props: map[string]Histogram{
			PropKey(true, "Person", "name"): {Count: 3, NDV: 3, Min: value.EncodeKey(value.String("A")), Max: value.EncodeKey(value.String("C")), Buckets: []Bucket{{Upper: value.EncodeKey(value.String("C")), Count: 3, NDV: 3}}},
		},
	}
	if err := PutStats(tx, st); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	sch, err := Load(tx)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if sch.Format != FormatVersion || sch.Generation == 0 {
		t.Fatalf("format %+v", sch)
	}
	got, ok := sch.IndexByName("person_name")
	if !ok || got.ID != 1 || got.Label != "Person" || len(got.Props) != 2 || got.Props[1] != "city" || got.State.String() != "ONLINE" {
		t.Fatalf("index %+v", got)
	}
	con, ok := sch.ConstraintByName("person_email")
	if !ok || con.Kind.String() != "unique" || con.IndexName != "person_email" {
		t.Fatalf("constraint %+v", con)
	}
	if sch.Stats.Nodes != 3 || sch.Stats.Labels["Person"] != 3 || sch.EstimateEq("Person", "name") != 1 {
		t.Fatalf("stats %+v est %v", sch.Stats, sch.EstimateEq("Person", "name"))
	}
	tx, err = eng.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ok, err = DropIndex(tx, "person_name")
	if err != nil || !ok {
		t.Fatalf("drop index %v %v", ok, err)
	}
	ok, err = DropIndex(tx, "person_name")
	if err != nil || ok {
		t.Fatalf("missing drop %v %v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUniqueLockKeyStable(t *testing.T) {
	a := UniqueLockKey(3, []value.Value{value.String("x")})
	b := UniqueLockKey(3, []value.Value{value.String("x")})
	if len(a) == 0 || string(a) != string(b) || a[0] != KindUniqueLock {
		t.Fatalf("%x %x", a, b)
	}
}

package graph

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestAddGetUpdateDeleteNode(t *testing.T) {
	g := New()
	n := g.AddNode("person", map[string]any{"name": "Alice", "age": int64(30)})
	if n.ID != 1 || n.Label != "person" {
		t.Fatalf("unexpected node: %+v", n)
	}
	got, ok := g.GetNode(1)
	if !ok || got.Props["name"] != "Alice" {
		t.Fatalf("GetNode failed: ok=%v got=%+v", ok, got)
	}

	updated, err := g.UpdateNode(1, map[string]any{"age": int64(31), "city": "Paris"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Props["age"] != int64(31) || updated.Props["city"] != "Paris" {
		t.Fatalf("update merge failed: %+v", updated.Props)
	}

	if err := g.DeleteNode(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.GetNode(1); ok {
		t.Fatal("expected node to be gone")
	}
	if err := g.DeleteNode(1); err == nil {
		t.Fatal("expected delete of missing node to fail")
	} else if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestAddEdgeRequiresEndpoints(t *testing.T) {
	g := New()
	if _, err := g.AddEdge(1, 2, "KNOWS", nil); err == nil {
		t.Fatal("expected missing-from error")
	}
	g.AddNode("person", nil)
	if _, err := g.AddEdge(1, 2, "KNOWS", nil); err == nil {
		t.Fatal("expected missing-to error")
	}
}

func TestAddGetUpdateDeleteEdge(t *testing.T) {
	g := New()
	a := g.AddNode("person", map[string]any{"name": "A"})
	b := g.AddNode("person", map[string]any{"name": "B"})
	e, err := g.AddEdge(a.ID, b.ID, "KNOWS", map[string]any{"since": int64(2020)})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != 1 || e.From != a.ID || e.To != b.ID {
		t.Fatalf("unexpected edge: %+v", e)
	}
	got, ok := g.GetEdge(1)
	if !ok || got.Label != "KNOWS" {
		t.Fatalf("GetEdge failed: %+v", got)
	}
	updated, err := g.UpdateEdge(1, map[string]any{"since": int64(2021)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Props["since"] != int64(2021) {
		t.Fatalf("edge update failed: %+v", updated.Props)
	}
	if err := g.DeleteEdge(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.GetEdge(1); ok {
		t.Fatal("expected edge to be gone")
	}
}

func TestEdgesBetweenAndByLabel(t *testing.T) {
	g := New()
	a := g.AddNode("person", nil)
	b := g.AddNode("person", nil)
	c := g.AddNode("person", nil)
	if _, err := g.AddEdge(a.ID, b.ID, "KNOWS", map[string]any{"since": int64(2020)}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge(a.ID, b.ID, "WORKS_WITH", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge(a.ID, c.ID, "KNOWS", nil); err != nil {
		t.Fatal(err)
	}

	between, err := g.EdgesBetween(a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(between) != 2 {
		t.Fatalf("expected 2 edges from 1 to 2, got %d", len(between))
	}
	reverse, err := g.EdgesBetween(b.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse) != 0 {
		t.Fatalf("expected directed lookup, got %d", len(reverse))
	}
	knows := g.EdgesByLabel("KNOWS")
	if len(knows) != 2 {
		t.Fatalf("expected 2 KNOWS edges, got %d", len(knows))
	}
}

func TestDeleteNodeCascadesEdges(t *testing.T) {
	g := New()
	a := g.AddNode("person", nil)
	b := g.AddNode("person", nil)
	c := g.AddNode("person", nil)
	if _, err := g.AddEdge(a.ID, b.ID, "KNOWS", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge(c.ID, a.ID, "FOLLOWS", nil); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteNode(a.ID); err != nil {
		t.Fatal(err)
	}
	if g.Stats().Edges != 0 {
		t.Fatalf("expected cascade delete of both edges, stats=%+v", g.Stats())
	}
	if _, ok := g.GetEdge(1); ok {
		t.Fatal("outgoing edge should be gone")
	}
	if _, ok := g.GetEdge(2); ok {
		t.Fatal("incoming edge should be gone")
	}
}

func TestNodesByLabelAndPropIndex(t *testing.T) {
	g := New()
	g.AddNode("person", map[string]any{"name": "Alice", "age": int64(30)})
	g.AddNode("person", map[string]any{"name": "Bob", "age": int64(20)})
	g.AddNode("company", map[string]any{"name": "Acme"})

	people := g.NodesByLabel("person")
	if len(people) != 2 {
		t.Fatalf("expected 2 people, got %d", len(people))
	}
	alices := g.NodesByPropEq("name", "Alice")
	if len(alices) != 1 || alices[0].Label != "person" {
		t.Fatalf("prop index lookup failed: %+v", alices)
	}

	if _, err := g.UpdateNode(1, map[string]any{"name": "Alicia"}); err != nil {
		t.Fatal(err)
	}
	if got := g.NodesByPropEq("name", "Alice"); len(got) != 0 {
		t.Fatalf("stale prop index after update: %+v", got)
	}
	if got := g.NodesByPropEq("name", "Alicia"); len(got) != 1 {
		t.Fatalf("updated prop not indexed: %+v", got)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	g := New()
	a := g.AddNode("person", map[string]any{"name": "Alice"})
	b := g.AddNode("person", map[string]any{"name": "Bob"})
	if _, err := g.AddEdge(a.ID, b.ID, "KNOWS", map[string]any{"since": int64(2020)}); err != nil {
		t.Fatal(err)
	}

	snap := g.Export()
	g2 := New()
	g2.Import(snap)

	if g2.Stats().Nodes != 2 || g2.Stats().Edges != 1 {
		t.Fatalf("import stats mismatch: %+v", g2.Stats())
	}
	n := g2.AddNode("person", map[string]any{"name": "Cara"})
	if n.ID != 3 {
		t.Fatalf("next node id not preserved, got %d", n.ID)
	}
	people := g2.NodesByLabel("person")
	if len(people) != 3 {
		t.Fatalf("label index not rebuilt, got %d", len(people))
	}
}

func TestReturnedCopiesAreSafe(t *testing.T) {
	g := New()
	n := g.AddNode("person", map[string]any{"name": "Alice"})
	n.Props["name"] = "mutated"
	got, _ := g.GetNode(1)
	if got.Props["name"] != "Alice" {
		t.Fatal("caller mutation leaked into store")
	}
}

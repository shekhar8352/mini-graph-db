package graph

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func chain(t *testing.T) *Graph {
	t.Helper()
	g := New()
	a := g.AddNode("n", map[string]any{"name": "A"})
	b := g.AddNode("n", map[string]any{"name": "B"})
	c := g.AddNode("n", map[string]any{"name": "C"})
	d := g.AddNode("n", map[string]any{"name": "D"})
	mustEdge(t, g, a.ID, b.ID, "TO")
	mustEdge(t, g, b.ID, c.ID, "TO")
	mustEdge(t, g, a.ID, d.ID, "TO")
	return g
}

func mustEdge(t *testing.T, g *Graph, from, to uint64, label string) {
	t.Helper()
	if _, err := g.AddEdge(from, to, label, nil); err != nil {
		t.Fatal(err)
	}
}

func names(ns []Neighbor) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Node.Props["name"].(string)
	}
	return out
}

func TestNeighborsBFSDepth(t *testing.T) {
	g := chain(t)
	d1, err := g.Neighbors(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d1) != 2 {
		t.Fatalf("depth 1 expected B,D got %v", names(d1))
	}
	d2, err := g.Neighbors(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d2) != 3 {
		t.Fatalf("depth 2 expected B,D,C got %v", names(d2))
	}
}

func TestDFSOrder(t *testing.T) {
	g := chain(t)
	got, err := g.DFS(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 nodes, got %v", names(got))
	}
}

func TestShortestPath(t *testing.T) {
	g := chain(t)
	path, err := g.ShortestPath(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 || path[0].ID != 1 || path[1].ID != 2 || path[2].ID != 3 {
		t.Fatalf("unexpected path: %+v", path)
	}

	none, err := g.ShortestPath(3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no reverse path, got %+v", none)
	}

	self, err := g.ShortestPath(1, 1)
	if err != nil || len(self) != 1 || self[0].ID != 1 {
		t.Fatalf("self path: %+v err=%v", self, err)
	}
}

func TestTraversalMissingNode(t *testing.T) {
	g := New()
	if _, err := g.Neighbors(9, 1); err == nil {
		t.Fatal("expected missing start")
	} else if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
	if _, err := g.ShortestPath(1, 2); err == nil {
		t.Fatal("expected missing src")
	} else if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestNeighborsInvalidDepth(t *testing.T) {
	g := New()
	g.AddNode("n", nil)
	if _, err := g.Neighbors(1, 0); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

package graph

import (
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
)

// Neighbor is a node reached during a bounded traversal, with its hop distance.
type Neighbor struct {
	Node  Node
	Depth int
}

// Neighbors returns nodes reachable from start by following outgoing edges,
// up to the given depth (inclusive). The start node itself is omitted.
// Depth must be at least 1.
func (g *Graph) Neighbors(start uint64, depth int) ([]Neighbor, error) {
	if depth < 1 {
		return nil, gerr.New(gerr.InvalidArgument, "depth must be >= 1")
	}
	var got []graphstore.Neighbor
	err := g.read(func(tx storage.Tx) error {
		var err error
		got, err = graphstore.Neighbors(tx, start, depth)
		return err
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Neighbor, len(got))
	for i, n := range got {
		out[i] = Neighbor{Node: toNode(n.Node), Depth: n.Depth}
	}
	return out, nil
}

// DFS returns nodes visited in depth-first preorder along outgoing edges,
// up to the given depth. The start node itself is omitted.
func (g *Graph) DFS(start uint64, depth int) ([]Neighbor, error) {
	if depth < 1 {
		return nil, gerr.New(gerr.InvalidArgument, "depth must be >= 1")
	}
	var got []graphstore.Neighbor
	err := g.read(func(tx storage.Tx) error {
		var err error
		got, err = graphstore.DFS(tx, start, depth)
		return err
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Neighbor, len(got))
	for i, n := range got {
		out[i] = Neighbor{Node: toNode(n.Node), Depth: n.Depth}
	}
	return out, nil
}

// ShortestPath returns the unweighted shortest path from src to dst as a
// sequence of nodes (inclusive). The slice is empty when no path exists.
func (g *Graph) ShortestPath(src, dst uint64) ([]Node, error) {
	var path []graphstore.Node
	err := g.read(func(tx storage.Tx) error {
		var err error
		path, err = graphstore.ShortestPath(tx, src, dst)
		return err
	})
	if err != nil {
		return nil, mapErr(err)
	}
	if len(path) == 0 {
		return []Node{}, nil
	}
	return toNodes(path), nil
}

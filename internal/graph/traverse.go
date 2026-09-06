package graph

import "fmt"

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
		return nil, fmt.Errorf("depth must be >= 1")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[start]; !ok {
		return nil, fmt.Errorf("node %d not found", start)
	}

	type item struct {
		id    uint64
		depth int
	}
	seen := map[uint64]struct{}{start: {}}
	queue := []item{{id: start, depth: 0}}
	var out []Neighbor

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= depth {
			continue
		}
		for _, eid := range g.outEdges[cur.id] {
			e := g.edges[eid]
			if e == nil {
				continue
			}
			if _, ok := seen[e.To]; ok {
				continue
			}
			seen[e.To] = struct{}{}
			n := g.nodes[e.To]
			if n == nil {
				continue
			}
			nextDepth := cur.depth + 1
			out = append(out, Neighbor{Node: n.clone(), Depth: nextDepth})
			queue = append(queue, item{id: e.To, depth: nextDepth})
		}
	}
	return out, nil
}

// DFS returns nodes visited in depth-first preorder along outgoing edges,
// up to the given depth. The start node itself is omitted.
func (g *Graph) DFS(start uint64, depth int) ([]Neighbor, error) {
	if depth < 1 {
		return nil, fmt.Errorf("depth must be >= 1")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[start]; !ok {
		return nil, fmt.Errorf("node %d not found", start)
	}

	seen := map[uint64]struct{}{start: {}}
	var out []Neighbor

	var walk func(id uint64, d int)
	walk = func(id uint64, d int) {
		if d >= depth {
			return
		}
		for _, eid := range g.outEdges[id] {
			e := g.edges[eid]
			if e == nil {
				continue
			}
			if _, ok := seen[e.To]; ok {
				continue
			}
			n := g.nodes[e.To]
			if n == nil {
				continue
			}
			seen[e.To] = struct{}{}
			next := d + 1
			out = append(out, Neighbor{Node: n.clone(), Depth: next})
			walk(e.To, next)
		}
	}
	walk(start, 0)
	return out, nil
}

// ShortestPath returns the unweighted shortest path from src to dst as a
// sequence of nodes (inclusive). The slice is empty when no path exists.
func (g *Graph) ShortestPath(src, dst uint64) ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[src]; !ok {
		return nil, fmt.Errorf("node %d not found", src)
	}
	if _, ok := g.nodes[dst]; !ok {
		return nil, fmt.Errorf("node %d not found", dst)
	}
	if src == dst {
		return []Node{g.nodes[src].clone()}, nil
	}

	parent := map[uint64]uint64{}
	seen := map[uint64]struct{}{src: {}}
	queue := []uint64{src}

	found := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, eid := range g.outEdges[cur] {
			e := g.edges[eid]
			if e == nil {
				continue
			}
			if _, ok := seen[e.To]; ok {
				continue
			}
			seen[e.To] = struct{}{}
			parent[e.To] = cur
			if e.To == dst {
				found = true
				break
			}
			queue = append(queue, e.To)
		}
		if found {
			break
		}
	}
	if !found {
		return []Node{}, nil
	}

	var ids []uint64
	for at := dst; ; at = parent[at] {
		ids = append(ids, at)
		if at == src {
			break
		}
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	path := make([]Node, 0, len(ids))
	for _, id := range ids {
		path = append(path, g.nodes[id].clone())
	}
	return path, nil
}

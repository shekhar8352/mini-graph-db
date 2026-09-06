package graph

import (
	"fmt"
	"sync"
)

// Node is a labeled vertex with arbitrary key-value properties.
type Node struct {
	ID    uint64
	Label string
	Props map[string]any
}

// Edge is a directed, labeled relationship between two nodes.
type Edge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	Props map[string]any
}

// Stats is a snapshot of graph cardinality.
type Stats struct {
	Nodes  int
	Edges  int
	Labels []string
}

// Snapshot is a serializable copy of the full graph, including ID counters.
type Snapshot struct {
	Nodes    []Node
	Edges    []Edge
	NextNode uint64
	NextEdge uint64
}

// Graph is an in-memory property graph with adjacency lists and indexes.
type Graph struct {
	mu         sync.RWMutex
	nodes      map[uint64]*Node
	edges      map[uint64]*Edge
	outEdges   map[uint64][]uint64
	inEdges    map[uint64][]uint64
	labelIndex map[string]map[uint64]struct{}
	propIndex  map[string]map[string]map[uint64]struct{}
	nextNode   uint64
	nextEdge   uint64
}

// New returns an empty graph. Node and edge IDs start at 1.
func New() *Graph {
	return &Graph{
		nodes:      make(map[uint64]*Node),
		edges:      make(map[uint64]*Edge),
		outEdges:   make(map[uint64][]uint64),
		inEdges:    make(map[uint64][]uint64),
		labelIndex: make(map[string]map[uint64]struct{}),
		propIndex:  make(map[string]map[string]map[uint64]struct{}),
		nextNode:   1,
		nextEdge:   1,
	}
}

// AddNode creates a node and returns a copy of it.
func (g *Graph) AddNode(label string, props map[string]any) Node {
	g.mu.Lock()
	defer g.mu.Unlock()

	n := &Node{
		ID:    g.nextNode,
		Label: label,
		Props: cloneProps(props),
	}
	g.nextNode++
	g.nodes[n.ID] = n
	g.indexNode(n)
	return n.clone()
}

// GetNode returns a copy of the node, or false if it does not exist.
func (g *Graph) GetNode(id uint64) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, false
	}
	return n.clone(), true
}

// UpdateNode merges props into an existing node. New keys are added; existing
// keys are overwritten. A nil/empty props map is a no-op.
func (g *Graph) UpdateNode(id uint64, props map[string]any) (Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return Node{}, fmt.Errorf("node %d not found", id)
	}
	g.unindexNode(n)
	for k, v := range props {
		n.Props[k] = v
	}
	g.indexNode(n)
	return n.clone(), nil
}

// DeleteNode removes a node and every incident edge.
func (g *Graph) DeleteNode(id uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("node %d not found", id)
	}

	incident := uniqueIDs(append(append([]uint64{}, g.outEdges[id]...), g.inEdges[id]...))
	for _, eid := range incident {
		g.deleteEdgeLocked(eid)
	}
	g.unindexNode(n)
	delete(g.nodes, id)
	delete(g.outEdges, id)
	delete(g.inEdges, id)
	return nil
}

// AddEdge creates a directed edge. Both endpoints must already exist.
func (g *Graph) AddEdge(from, to uint64, label string, props map[string]any) (Edge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.nodes[from]; !ok {
		return Edge{}, fmt.Errorf("from node %d not found", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return Edge{}, fmt.Errorf("to node %d not found", to)
	}

	e := &Edge{
		ID:    g.nextEdge,
		From:  from,
		To:    to,
		Label: label,
		Props: cloneProps(props),
	}
	g.nextEdge++
	g.edges[e.ID] = e
	g.outEdges[from] = append(g.outEdges[from], e.ID)
	g.inEdges[to] = append(g.inEdges[to], e.ID)
	return e.clone(), nil
}

// GetEdge returns a copy of the edge, or false if it does not exist.
func (g *Graph) GetEdge(id uint64) (Edge, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e, ok := g.edges[id]
	if !ok {
		return Edge{}, false
	}
	return e.clone(), true
}

// UpdateEdge merges props into an existing edge.
func (g *Graph) UpdateEdge(id uint64, props map[string]any) (Edge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.edges[id]
	if !ok {
		return Edge{}, fmt.Errorf("edge %d not found", id)
	}
	for k, v := range props {
		e.Props[k] = v
	}
	return e.clone(), nil
}

// DeleteEdge removes an edge. Endpoints are left intact.
func (g *Graph) DeleteEdge(id uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.edges[id]; !ok {
		return fmt.Errorf("edge %d not found", id)
	}
	g.deleteEdgeLocked(id)
	return nil
}

func (g *Graph) deleteEdgeLocked(id uint64) {
	e, ok := g.edges[id]
	if !ok {
		return
	}
	g.outEdges[e.From] = removeID(g.outEdges[e.From], id)
	g.inEdges[e.To] = removeID(g.inEdges[e.To], id)
	delete(g.edges, id)
}

// NodesByLabel returns copies of every node with the given label.
func (g *Graph) NodesByLabel(label string) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := g.labelIndex[label]
	out := make([]Node, 0, len(ids))
	for id := range ids {
		if n, ok := g.nodes[id]; ok {
			out = append(out, n.clone())
		}
	}
	return out
}

// NodesByPropEq returns nodes whose property equals value, using the property index.
func (g *Graph) NodesByPropEq(key string, value any) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	bucket := g.propIndex[key]
	if bucket == nil {
		return nil
	}
	ids := bucket[propKey(value)]
	out := make([]Node, 0, len(ids))
	for id := range ids {
		if n, ok := g.nodes[id]; ok {
			out = append(out, n.clone())
		}
	}
	return out
}

// AllNodes returns copies of every node.
func (g *Graph) AllNodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n.clone())
	}
	return out
}

// AllEdges returns copies of every edge.
func (g *Graph) AllEdges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		out = append(out, e.clone())
	}
	return out
}

// EdgesBetween returns directed edges from -> to. Both nodes must exist.
func (g *Graph) EdgesBetween(from, to uint64) ([]Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[from]; !ok {
		return nil, fmt.Errorf("node %d not found", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return nil, fmt.Errorf("node %d not found", to)
	}
	var out []Edge
	for _, eid := range g.outEdges[from] {
		e := g.edges[eid]
		if e != nil && e.To == to {
			out = append(out, e.clone())
		}
	}
	return out, nil
}

// EdgesByLabel returns copies of every edge with the given label.
func (g *Graph) EdgesByLabel(label string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for _, e := range g.edges {
		if e.Label == label {
			out = append(out, e.clone())
		}
	}
	return out
}

// Stats returns node/edge counts and the set of node labels.
func (g *Graph) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	labels := make([]string, 0, len(g.labelIndex))
	for l := range g.labelIndex {
		if len(g.labelIndex[l]) > 0 {
			labels = append(labels, l)
		}
	}
	return Stats{
		Nodes:  len(g.nodes),
		Edges:  len(g.edges),
		Labels: labels,
	}
}

// Export copies the full graph for persistence.
func (g *Graph) Export() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	nodes := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodes = append(nodes, n.clone())
	}
	edges := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		edges = append(edges, e.clone())
	}
	return Snapshot{
		Nodes:    nodes,
		Edges:    edges,
		NextNode: g.nextNode,
		NextEdge: g.nextEdge,
	}
}

// Import replaces the graph with a snapshot and rebuilds indexes.
func (g *Graph) Import(s Snapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.nodes = make(map[uint64]*Node, len(s.Nodes))
	g.edges = make(map[uint64]*Edge, len(s.Edges))
	g.outEdges = make(map[uint64][]uint64)
	g.inEdges = make(map[uint64][]uint64)
	g.labelIndex = make(map[string]map[uint64]struct{})
	g.propIndex = make(map[string]map[string]map[uint64]struct{})
	g.nextNode = s.NextNode
	g.nextEdge = s.NextEdge
	if g.nextNode == 0 {
		g.nextNode = 1
	}
	if g.nextEdge == 0 {
		g.nextEdge = 1
	}

	for _, n := range s.Nodes {
		cn := &Node{ID: n.ID, Label: n.Label, Props: cloneProps(n.Props)}
		g.nodes[cn.ID] = cn
		g.indexNode(cn)
		if cn.ID >= g.nextNode {
			g.nextNode = cn.ID + 1
		}
	}
	for _, e := range s.Edges {
		ce := &Edge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: cloneProps(e.Props)}
		g.edges[ce.ID] = ce
		g.outEdges[ce.From] = append(g.outEdges[ce.From], ce.ID)
		g.inEdges[ce.To] = append(g.inEdges[ce.To], ce.ID)
		if ce.ID >= g.nextEdge {
			g.nextEdge = ce.ID + 1
		}
	}
}

func (g *Graph) indexNode(n *Node) {
	set, ok := g.labelIndex[n.Label]
	if !ok {
		set = make(map[uint64]struct{})
		g.labelIndex[n.Label] = set
	}
	set[n.ID] = struct{}{}

	for k, v := range n.Props {
		pk := propKey(v)
		byVal, ok := g.propIndex[k]
		if !ok {
			byVal = make(map[string]map[uint64]struct{})
			g.propIndex[k] = byVal
		}
		ids, ok := byVal[pk]
		if !ok {
			ids = make(map[uint64]struct{})
			byVal[pk] = ids
		}
		ids[n.ID] = struct{}{}
	}
}

func (g *Graph) unindexNode(n *Node) {
	if set, ok := g.labelIndex[n.Label]; ok {
		delete(set, n.ID)
		if len(set) == 0 {
			delete(g.labelIndex, n.Label)
		}
	}
	for k, v := range n.Props {
		pk := propKey(v)
		if byVal, ok := g.propIndex[k]; ok {
			if ids, ok := byVal[pk]; ok {
				delete(ids, n.ID)
				if len(ids) == 0 {
					delete(byVal, pk)
				}
			}
			if len(byVal) == 0 {
				delete(g.propIndex, k)
			}
		}
	}
}

func (n Node) clone() Node {
	return Node{ID: n.ID, Label: n.Label, Props: cloneProps(n.Props)}
}

func (e Edge) clone() Edge {
	return Edge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: cloneProps(e.Props)}
}

func cloneProps(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func propKey(v any) string {
	return fmt.Sprintf("%T:%v", v, v)
}

func removeID(ids []uint64, target uint64) []uint64 {
	out := ids[:0]
	for _, id := range ids {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

func uniqueIDs(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

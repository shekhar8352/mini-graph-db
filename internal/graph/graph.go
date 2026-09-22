// Package graph is an in-memory directed property-graph engine.
package graph

import (
	"fmt"
	"sort"
	"sync"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Prop is one property stored under an interned key id.
// ID zero means the name has not been interned yet (import assigns one).
type Prop struct {
	ID    uint32
	Name  string
	Value value.Value
}

// Node is a vertex with a set of labels and typed properties.
// Labels and properties are read through accessors; the maps are not part of
// the public layout.
type Node struct {
	ID     uint64
	labels []string
	props  map[uint32]value.Value
	names  map[uint32]string
}

// Edge is a directed relationship. It has exactly one type, stored in Label.
type Edge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	props map[uint32]value.Value
	names map[uint32]string
}

// Stats is a snapshot of graph cardinality.
type Stats struct {
	Nodes  int
	Edges  int
	Labels []string
}

// Snapshot is a serializable copy of the full graph, including ID counters
// and the property-key intern table (index is the key id; entry 0 is unused).
type Snapshot struct {
	Nodes    []Node
	Edges    []Edge
	NextNode uint64
	NextEdge uint64
	PropKeys []string
}

// Graph is an in-memory property graph with adjacency lists and indexes.
type Graph struct {
	mu         sync.RWMutex
	nodes      map[uint64]*Node
	edges      map[uint64]*Edge
	outEdges   map[uint64][]uint64
	inEdges    map[uint64][]uint64
	labelIndex map[string]map[uint64]struct{}
	propIndex  map[uint32]map[string]map[uint64]struct{}
	catalog    *propCatalog
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
		propIndex:  make(map[uint32]map[string]map[uint64]struct{}),
		catalog:    newPropCatalog(),
		nextNode:   1,
		nextEdge:   1,
	}
}

// Label returns the lexicographically first label, or "" when the node has none.
func (n Node) Label() string {
	if len(n.labels) == 0 {
		return ""
	}
	return n.labels[0]
}

// Labels returns a copy of the label set, sorted and unique.
func (n Node) Labels() []string {
	return append([]string(nil), n.labels...)
}

// HasLabel reports whether the node carries label.
func (n Node) HasLabel(label string) bool {
	i := sort.SearchStrings(n.labels, label)
	return i < len(n.labels) && n.labels[i] == label
}

// Prop returns a property by name.
func (n Node) Prop(name string) (value.Value, bool) {
	return propGet(n.props, n.names, name)
}

// PropID returns the interned id of a property name on this node.
func (n Node) PropID(name string) (uint32, bool) {
	for id, propName := range n.names {
		if propName == name {
			return id, true
		}
	}
	return 0, false
}

// Properties returns a copy of the properties keyed by name.
func (n Node) Properties() map[string]value.Value {
	return propMap(n.props, n.names)
}

// PropList returns the properties sorted by key id.
func (n Node) PropList() []Prop {
	return propList(n.props, n.names)
}

// Prop returns a property by name.
func (e Edge) Prop(name string) (value.Value, bool) {
	return propGet(e.props, e.names, name)
}

// Properties returns a copy of the edge properties keyed by name.
func (e Edge) Properties() map[string]value.Value {
	return propMap(e.props, e.names)
}

// PropList returns the edge properties sorted by key id.
func (e Edge) PropList() []Prop {
	return propList(e.props, e.names)
}

// MakeNode builds a node value for import. Labels are sorted and de-duplicated.
// Property IDs may be zero; Import interns those names.
func MakeNode(id uint64, labels []string, props []Prop) Node {
	n := Node{ID: id, labels: normalizeLabels(labels)}
	n.props, n.names = propsToMaps(props)
	return n
}

// MakeEdge builds an edge value for import.
func MakeEdge(id, from, to uint64, label string, props []Prop) Edge {
	e := Edge{ID: id, From: from, To: to, Label: label}
	e.props, e.names = propsToMaps(props)
	return e
}

// InternPropKey returns the stable id for a property name, assigning one if needed.
func (g *Graph) InternPropKey(name string) uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.catalog.intern(name)
}

// PropKeyName returns the name for an interned property-key id.
func (g *Graph) PropKeyName(id uint32) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.catalog.name(id)
}

// AddNode creates a node with one label. Property values are the legacy Go
// scalars (bool, integer, float, string, bytes) plus lists and maps of those.
// Unknown Go types are stored as their printed string.
func (g *Graph) AddNode(label string, props map[string]any) Node {
	n, err := g.CreateNode([]string{label}, coerceAnyMap(props))
	if err != nil {
		// coerceAnyMap only produces storable values.
		return Node{}
	}
	return n
}

// CreateNode creates a node with the given labels and typed properties.
// Labels are stored sorted and unique. A Path property is rejected.
func (g *Graph) CreateNode(labels []string, props map[string]value.Value) (Node, error) {
	if err := validateProps(props); err != nil {
		return Node{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	n := &Node{
		ID:     g.nextNode,
		labels: normalizeLabels(labels),
	}
	g.nextNode++
	g.setProps(n, props)
	g.nodes[n.ID] = n
	g.indexNode(n)
	return n.clone(), nil
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
// keys are overwritten. A nil or empty props map is a no-op.
func (g *Graph) UpdateNode(id uint64, props map[string]any) (Node, error) {
	return g.UpdateNodeValues(id, coerceAnyMap(props))
}

// UpdateNodeValues merges typed properties into an existing node.
func (g *Graph) UpdateNodeValues(id uint64, props map[string]value.Value) (Node, error) {
	if len(props) == 0 {
		n, ok := g.GetNode(id)
		if !ok {
			return Node{}, gerr.Newf(gerr.NotFound, "node %d not found", id)
		}
		return n, nil
	}
	if err := validateProps(props); err != nil {
		return Node{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return Node{}, gerr.Newf(gerr.NotFound, "node %d not found", id)
	}
	g.unindexNode(n)
	if n.props == nil {
		n.props = map[uint32]value.Value{}
		n.names = map[uint32]string{}
	}
	g.mergeProps(n.props, n.names, props)
	g.indexNode(n)
	return n.clone(), nil
}

// DeleteNode removes a node and every incident edge.
func (g *Graph) DeleteNode(id uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return gerr.Newf(gerr.NotFound, "node %d not found", id)
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
	return g.CreateEdge(from, to, label, coerceAnyMap(props))
}

// CreateEdge creates a directed edge with typed properties.
func (g *Graph) CreateEdge(from, to uint64, label string, props map[string]value.Value) (Edge, error) {
	if err := validateProps(props); err != nil {
		return Edge{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.nodes[from]; !ok {
		return Edge{}, gerr.Newf(gerr.NotFound, "from node %d not found", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return Edge{}, gerr.Newf(gerr.NotFound, "to node %d not found", to)
	}

	e := &Edge{
		ID:    g.nextEdge,
		From:  from,
		To:    to,
		Label: label,
	}
	g.nextEdge++
	e.props, e.names = g.materialize(props)
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
	return g.UpdateEdgeValues(id, coerceAnyMap(props))
}

// UpdateEdgeValues merges typed properties into an existing edge.
func (g *Graph) UpdateEdgeValues(id uint64, props map[string]value.Value) (Edge, error) {
	if len(props) == 0 {
		e, ok := g.GetEdge(id)
		if !ok {
			return Edge{}, gerr.Newf(gerr.NotFound, "edge %d not found", id)
		}
		return e, nil
	}
	if err := validateProps(props); err != nil {
		return Edge{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.edges[id]
	if !ok {
		return Edge{}, gerr.Newf(gerr.NotFound, "edge %d not found", id)
	}
	if e.props == nil {
		e.props = map[uint32]value.Value{}
		e.names = map[uint32]string{}
	}
	g.mergeProps(e.props, e.names, props)
	return e.clone(), nil
}

// DeleteEdge removes an edge. Endpoints are left intact.
func (g *Graph) DeleteEdge(id uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.edges[id]; !ok {
		return gerr.Newf(gerr.NotFound, "edge %d not found", id)
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

// NodesByLabel returns copies of every node that carries label.
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

// NodesByPropEq returns nodes whose property equals value.
// Numeric equality is cross-type: int 1 matches float 1.0.
func (g *Graph) NodesByPropEq(key string, raw any) []Node {
	val, err := value.FromAny(raw)
	if err != nil || !val.Storable() {
		val = value.String(fmt.Sprint(raw))
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	id, ok := g.catalog.lookup(key)
	if !ok {
		return nil
	}
	bucket := g.propIndex[id]
	if bucket == nil {
		return nil
	}
	ids := bucket[string(value.EncodeKey(val))]
	out := make([]Node, 0, len(ids))
	for nid := range ids {
		if n, ok := g.nodes[nid]; ok {
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
		return nil, gerr.Newf(gerr.NotFound, "node %d not found", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return nil, gerr.Newf(gerr.NotFound, "node %d not found", to)
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

// EdgesByLabel returns copies of every edge with the given type.
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
		PropKeys: g.catalog.export(),
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
	g.propIndex = make(map[uint32]map[string]map[uint64]struct{})
	if len(s.PropKeys) > 0 {
		g.catalog = catalogFrom(s.PropKeys)
	} else {
		g.catalog = newPropCatalog()
	}
	g.nextNode = s.NextNode
	g.nextEdge = s.NextEdge
	if g.nextNode == 0 {
		g.nextNode = 1
	}
	if g.nextEdge == 0 {
		g.nextEdge = 1
	}

	for _, n := range s.Nodes {
		cn := &Node{ID: n.ID, labels: normalizeLabels(n.labels)}
		cn.props, cn.names = g.bindProps(n.PropList())
		g.nodes[cn.ID] = cn
		g.indexNode(cn)
		if cn.ID >= g.nextNode {
			g.nextNode = cn.ID + 1
		}
	}
	for _, e := range s.Edges {
		ce := &Edge{ID: e.ID, From: e.From, To: e.To, Label: e.Label}
		ce.props, ce.names = g.bindProps(e.PropList())
		g.edges[ce.ID] = ce
		g.outEdges[ce.From] = append(g.outEdges[ce.From], ce.ID)
		g.inEdges[ce.To] = append(g.inEdges[ce.To], ce.ID)
		if ce.ID >= g.nextEdge {
			g.nextEdge = ce.ID + 1
		}
	}
}

func (g *Graph) setProps(n *Node, props map[string]value.Value) {
	n.props = make(map[uint32]value.Value, len(props))
	n.names = make(map[uint32]string, len(props))
	g.mergeProps(n.props, n.names, props)
}

func (g *Graph) materialize(props map[string]value.Value) (map[uint32]value.Value, map[uint32]string) {
	vals := make(map[uint32]value.Value, len(props))
	names := make(map[uint32]string, len(props))
	g.mergeProps(vals, names, props)
	return vals, names
}

func (g *Graph) mergeProps(vals map[uint32]value.Value, names map[uint32]string, props map[string]value.Value) {
	for name, v := range props {
		id := g.catalog.intern(name)
		vals[id] = v
		names[id] = name
	}
}

func (g *Graph) bindProps(props []Prop) (map[uint32]value.Value, map[uint32]string) {
	vals := make(map[uint32]value.Value, len(props))
	names := make(map[uint32]string, len(props))
	for _, p := range props {
		id := p.ID
		name := p.Name
		if known, ok := g.catalog.name(id); ok {
			if name == "" {
				name = known
			}
		} else {
			id = g.catalog.intern(name)
		}
		vals[id] = p.Value
		names[id] = name
	}
	return vals, names
}

func (g *Graph) indexNode(n *Node) {
	for _, label := range n.labels {
		set, ok := g.labelIndex[label]
		if !ok {
			set = make(map[uint64]struct{})
			g.labelIndex[label] = set
		}
		set[n.ID] = struct{}{}
	}
	for id, v := range n.props {
		pk := string(value.EncodeKey(v))
		byVal, ok := g.propIndex[id]
		if !ok {
			byVal = make(map[string]map[uint64]struct{})
			g.propIndex[id] = byVal
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
	for _, label := range n.labels {
		if set, ok := g.labelIndex[label]; ok {
			delete(set, n.ID)
			if len(set) == 0 {
				delete(g.labelIndex, label)
			}
		}
	}
	for id, v := range n.props {
		pk := string(value.EncodeKey(v))
		if byVal, ok := g.propIndex[id]; ok {
			if ids, ok := byVal[pk]; ok {
				delete(ids, n.ID)
				if len(ids) == 0 {
					delete(byVal, pk)
				}
			}
			if len(byVal) == 0 {
				delete(g.propIndex, id)
			}
		}
	}
}

func (n *Node) clone() Node {
	if n == nil {
		return Node{}
	}
	cp := Node{
		ID:     n.ID,
		labels: append([]string(nil), n.labels...),
		props:  make(map[uint32]value.Value, len(n.props)),
		names:  make(map[uint32]string, len(n.names)),
	}
	for id, v := range n.props {
		cp.props[id] = v
	}
	for id, name := range n.names {
		cp.names[id] = name
	}
	return cp
}

func (e *Edge) clone() Edge {
	if e == nil {
		return Edge{}
	}
	cp := Edge{
		ID:    e.ID,
		From:  e.From,
		To:    e.To,
		Label: e.Label,
		props: make(map[uint32]value.Value, len(e.props)),
		names: make(map[uint32]string, len(e.names)),
	}
	for id, v := range e.props {
		cp.props[id] = v
	}
	for id, name := range e.names {
		cp.names[id] = name
	}
	return cp
}

func normalizeLabels(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, l := range in {
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func validateProps(props map[string]value.Value) error {
	for name, v := range props {
		if !v.Storable() {
			return gerr.Newf(gerr.InvalidArgument, "property %q is not storable", name)
		}
	}
	return nil
}

func coerceAnyMap(m map[string]any) map[string]value.Value {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]value.Value, len(m))
	for k, v := range m {
		val, err := value.FromAny(v)
		if err != nil || !val.Storable() {
			val = value.String(fmt.Sprint(v))
		}
		out[k] = val
	}
	return out
}

func propsToMaps(props []Prop) (map[uint32]value.Value, map[uint32]string) {
	used := make(map[uint32]struct{}, len(props))
	for _, p := range props {
		if p.ID != 0 {
			used[p.ID] = struct{}{}
		}
	}
	vals := make(map[uint32]value.Value, len(props))
	names := make(map[uint32]string, len(props))
	var gen uint32
	for _, p := range props {
		id := p.ID
		if id == 0 {
			for {
				gen++
				if _, taken := used[gen]; !taken {
					break
				}
			}
			id = gen
		}
		used[id] = struct{}{}
		vals[id] = p.Value
		names[id] = p.Name
	}
	return vals, names
}

func propGet(props map[uint32]value.Value, names map[uint32]string, name string) (value.Value, bool) {
	for id, n := range names {
		if n == name {
			v, ok := props[id]
			return v, ok
		}
	}
	return value.Value{}, false
}

func propMap(props map[uint32]value.Value, names map[uint32]string) map[string]value.Value {
	out := make(map[string]value.Value, len(props))
	for id, v := range props {
		out[names[id]] = v
	}
	return out
}

func propList(props map[uint32]value.Value, names map[uint32]string) []Prop {
	out := make([]Prop, 0, len(props))
	for id, v := range props {
		out = append(out, Prop{ID: id, Name: names[id], Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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

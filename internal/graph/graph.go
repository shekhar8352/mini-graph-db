// Package graph is the property-graph facade over the storage engine.
// Without Begin, each method auto-commits one transaction.
// Records, indexes, cascade delete, and traversals live in graphstore.
package graph

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Graph is a property graph stored in a storage.Engine.
// Node and edge IDs start at 1 and are never reused.
// Without Begin, each method auto-commits. Begin holds one transaction
// across methods until Commit or Rollback. That transaction is not safe
// for concurrent use.
type Graph struct {
	eng    storage.Engine
	mu     sync.Mutex
	held   storage.Tx
	heldRO bool
}

// New returns an empty graph backed by the in-memory engine.
func New() *Graph {
	return &Graph{eng: memory.Open()}
}

// NewWith binds a graph to eng. The caller closes eng.
func NewWith(eng storage.Engine) *Graph {
	return &Graph{eng: eng}
}

// Begin starts an explicit transaction. Later calls use it until Commit or Rollback.
// A second Begin fails. readOnly rejects later writes.
func (g *Graph) Begin(readOnly bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held != nil {
		return gerr.New(gerr.InvalidArgument, "transaction already open")
	}
	tx, err := g.eng.Begin(storage.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return err
	}
	g.held = tx
	g.heldRO = readOnly
	return nil
}

// Commit publishes the explicit transaction.
// A conflict or a failed pre-commit hook leaves the transaction open.
func (g *Graph) Commit() error {
	g.mu.Lock()
	tx := g.held
	ro := g.heldRO
	g.held = nil
	g.heldRO = false
	g.mu.Unlock()
	if tx == nil {
		return gerr.New(gerr.InvalidArgument, "no transaction")
	}
	err := tx.Commit()
	if err != nil && txOpen(tx) {
		g.mu.Lock()
		if g.held == nil {
			g.held = tx
			g.heldRO = ro
		}
		g.mu.Unlock()
	}
	return err
}

// Rollback drops the explicit transaction.
func (g *Graph) Rollback() error {
	g.mu.Lock()
	tx := g.held
	g.held = nil
	g.heldRO = false
	g.mu.Unlock()
	if tx == nil {
		return gerr.New(gerr.InvalidArgument, "no transaction")
	}
	return tx.Rollback()
}

// InReadOnlyTxn reports whether an explicit read-only transaction is open.
func (g *Graph) InReadOnlyTxn() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.held != nil && g.heldRO
}

// Vacuum asks the engine to drop versions no open snapshot can read.
// Engines without a transaction manager return InvalidArgument.
func (g *Graph) Vacuum(ctx context.Context) (uint64, error) {
	type vacuumer interface {
		Vacuum(context.Context) (uint64, error)
	}
	v, ok := g.eng.(vacuumer)
	if !ok {
		return 0, gerr.New(gerr.InvalidArgument, "VACUUM is not supported by this engine")
	}
	return v.Vacuum(ctx)
}

func txOpen(tx storage.Tx) bool {
	_, err := tx.Get(storage.KSCatalog, []byte{0xff})
	return !errors.Is(err, storage.ErrDone)
}

// InternPropKey returns the stable id for a property name, assigning one if needed.
func (g *Graph) InternPropKey(name string) uint32 {
	var id uint32
	err := g.write(func(tx storage.Tx) error {
		var e error
		id, e = graphstore.InternProp(tx, name)
		return e
	})
	if err != nil {
		return 0
	}
	return id
}

// PropKeyName returns the name for an interned property-key id.
func (g *Graph) PropKeyName(id uint32) (string, bool) {
	var (
		name string
		ok   bool
	)
	err := g.read(func(tx storage.Tx) error {
		var e error
		name, ok, e = graphstore.PropName(tx, id)
		return e
	})
	if err != nil {
		return "", false
	}
	return name, ok
}

// AddNode creates a node with one label. Property values are the legacy Go
// scalars (bool, integer, float, string, bytes) plus lists and maps of those.
// Unknown Go types are stored as their printed string.
func (g *Graph) AddNode(label string, props map[string]any) Node {
	n, err := g.CreateNode([]string{label}, coerceAnyMap(props))
	if err != nil {
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
	var n graphstore.Node
	err := g.write(func(tx storage.Tx) error {
		var e error
		n, e = graphstore.CreateNode(tx, labels, props)
		return e
	})
	if err != nil {
		return Node{}, mapErr(err)
	}
	return toNode(n), nil
}

// GetNode returns a copy of the node, or false if it does not exist.
func (g *Graph) GetNode(id uint64) (Node, bool) {
	n, ok := g.readNode(id)
	return n, ok
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
	var n graphstore.Node
	err := g.write(func(tx storage.Tx) error {
		var e error
		n, e = graphstore.UpdateNode(tx, id, props)
		return e
	})
	if err != nil {
		return Node{}, mapErr(err)
	}
	return toNode(n), nil
}

// DeleteNode removes a node and every incident edge.
func (g *Graph) DeleteNode(id uint64) error {
	return mapErr(g.write(func(tx storage.Tx) error {
		return graphstore.DeleteNode(tx, id)
	}))
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
	var e graphstore.Edge
	err := g.write(func(tx storage.Tx) error {
		var err error
		e, err = graphstore.CreateEdge(tx, from, to, label, props)
		return err
	})
	if err != nil {
		return Edge{}, mapErr(err)
	}
	return toEdge(e), nil
}

// GetEdge returns a copy of the edge, or false if it does not exist.
func (g *Graph) GetEdge(id uint64) (Edge, bool) {
	var e graphstore.Edge
	var found bool
	err := g.read(func(tx storage.Tx) error {
		var err error
		e, err = graphstore.GetEdge(tx, id)
		if graphstore.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return Edge{}, false
	}
	return toEdge(e), true
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
	var e graphstore.Edge
	err := g.write(func(tx storage.Tx) error {
		var err error
		e, err = graphstore.UpdateEdge(tx, id, props)
		return err
	})
	if err != nil {
		return Edge{}, mapErr(err)
	}
	return toEdge(e), nil
}

// DeleteEdge removes an edge. Endpoints are left intact.
func (g *Graph) DeleteEdge(id uint64) error {
	return mapErr(g.write(func(tx storage.Tx) error {
		return graphstore.DeleteEdge(tx, id)
	}))
}

// NodesByLabel returns copies of every node that carries label.
func (g *Graph) NodesByLabel(label string) []Node {
	var nodes []graphstore.Node
	_ = g.read(func(tx storage.Tx) error {
		var err error
		nodes, err = graphstore.NodesByLabel(tx, label)
		return err
	})
	return toNodes(nodes)
}

// NodesByPropEq returns nodes whose property equals value.
// Numeric equality is cross-type: int 1 matches float 1.0.
func (g *Graph) NodesByPropEq(key string, raw any) []Node {
	val, err := value.FromAny(raw)
	if err != nil || !val.Storable() {
		val = value.String(fmt.Sprint(raw))
	}
	var nodes []graphstore.Node
	_ = g.read(func(tx storage.Tx) error {
		var err error
		nodes, err = graphstore.NodesByPropEq(tx, key, val)
		return err
	})
	return toNodes(nodes)
}

// AllNodes returns copies of every node.
func (g *Graph) AllNodes() []Node {
	var nodes []graphstore.Node
	_ = g.read(func(tx storage.Tx) error {
		var err error
		nodes, err = graphstore.AllNodes(tx)
		return err
	})
	return toNodes(nodes)
}

// AllEdges returns copies of every edge.
func (g *Graph) AllEdges() []Edge {
	var edges []graphstore.Edge
	_ = g.read(func(tx storage.Tx) error {
		var err error
		edges, err = graphstore.AllEdges(tx)
		return err
	})
	return toEdges(edges)
}

// EdgesBetween returns directed edges from -> to. Both nodes must exist.
func (g *Graph) EdgesBetween(from, to uint64) ([]Edge, error) {
	var edges []graphstore.Edge
	err := g.read(func(tx storage.Tx) error {
		var err error
		edges, err = graphstore.EdgesBetween(tx, from, to)
		return err
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return toEdges(edges), nil
}

// EdgesByLabel returns copies of every edge with the given type.
func (g *Graph) EdgesByLabel(label string) []Edge {
	var edges []graphstore.Edge
	_ = g.read(func(tx storage.Tx) error {
		var err error
		edges, err = graphstore.EdgesByLabel(tx, label)
		return err
	})
	return toEdges(edges)
}

// Stats returns node/edge counts and the set of node labels.
func (g *Graph) Stats() Stats {
	var st graphstore.Stats
	_ = g.read(func(tx storage.Tx) error {
		var err error
		st, err = graphstore.GraphStats(tx)
		return err
	})
	return Stats{Nodes: st.Nodes, Edges: st.Edges, Labels: append([]string(nil), st.Labels...)}
}

// Export copies the full graph for persistence.
func (g *Graph) Export() Snapshot {
	var snap graphstore.Snapshot
	err := g.read(func(tx storage.Tx) error {
		var err error
		snap, err = graphstore.Export(tx)
		return err
	})
	if err != nil {
		return Snapshot{}
	}
	out := Snapshot{
		NextNode: snap.NextNode,
		NextEdge: snap.NextEdge,
		PropKeys: append([]string(nil), snap.PropKeys...),
	}
	out.Nodes = toNodes(snap.Nodes)
	out.Edges = toEdges(snap.Edges)
	return out
}

// Import replaces the graph with a snapshot and rebuilds indexes.
func (g *Graph) Import(s Snapshot) error {
	snap := graphstore.Snapshot{
		NextNode: s.NextNode,
		NextEdge: s.NextEdge,
		PropKeys: append([]string(nil), s.PropKeys...),
	}
	for _, n := range s.Nodes {
		snap.Nodes = append(snap.Nodes, toStoreNode(n))
	}
	for _, e := range s.Edges {
		snap.Edges = append(snap.Edges, toStoreEdge(e))
	}
	return g.write(func(tx storage.Tx) error {
		return graphstore.Replace(tx, snap)
	})
}

func (g *Graph) readNode(id uint64) (Node, bool) {
	var n graphstore.Node
	var found bool
	err := g.read(func(tx storage.Tx) error {
		var err error
		n, err = graphstore.GetNode(tx, id)
		if graphstore.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return Node{}, false
	}
	return toNode(n), true
}

func (g *Graph) read(fn func(storage.Tx) error) error {
	g.mu.Lock()
	held := g.held
	g.mu.Unlock()
	if held != nil {
		return fn(held)
	}
	tx, err := g.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	err = fn(tx)
	_ = tx.Rollback()
	return err
}

func (g *Graph) write(fn func(storage.Tx) error) error {
	g.mu.Lock()
	held := g.held
	heldRO := g.heldRO
	g.mu.Unlock()
	if held != nil {
		if heldRO {
			return storage.ErrReadOnly
		}
		return fn(held)
	}
	tx, err := g.eng.Begin(storage.TxOptions{})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var ge *gerr.Error
	if errors.As(err, &ge) {
		return err
	}
	if errors.Is(err, storage.ErrReadOnly) {
		return gerr.New(gerr.InvalidArgument, "read-only transaction")
	}
	if errors.Is(err, storage.ErrDone) {
		return gerr.New(gerr.InvalidArgument, "transaction finished")
	}
	var se *graphstore.Error
	if errors.As(err, &se) {
		switch {
		case se.NotFound():
			return gerr.New(gerr.NotFound, se.Error())
		case se.Invalid():
			return gerr.New(gerr.InvalidArgument, se.Error())
		}
	}
	return gerr.Wrap(gerr.Internal, "storage", err)
}

func toNode(n graphstore.Node) Node {
	props := make([]Prop, len(n.Props))
	for i, p := range n.Props {
		props[i] = Prop{ID: p.ID, Name: p.Name, Value: p.Value}
	}
	return MakeNode(n.ID, n.Labels, props)
}

func toEdge(e graphstore.Edge) Edge {
	props := make([]Prop, len(e.Props))
	for i, p := range e.Props {
		props[i] = Prop{ID: p.ID, Name: p.Name, Value: p.Value}
	}
	return MakeEdge(e.ID, e.From, e.To, e.Label, props)
}

func toNodes(in []graphstore.Node) []Node {
	out := make([]Node, len(in))
	for i, n := range in {
		out[i] = toNode(n)
	}
	return out
}

func toEdges(in []graphstore.Edge) []Edge {
	out := make([]Edge, len(in))
	for i, e := range in {
		out[i] = toEdge(e)
	}
	return out
}

func toStoreNode(n Node) graphstore.Node {
	pl := n.PropList()
	props := make([]graphstore.Prop, len(pl))
	for i, p := range pl {
		props[i] = graphstore.Prop{ID: p.ID, Name: p.Name, Value: p.Value}
	}
	return graphstore.Node{ID: n.ID, Labels: n.Labels(), Props: props}
}

func toStoreEdge(e Edge) graphstore.Edge {
	pl := e.PropList()
	props := make([]graphstore.Prop, len(pl))
	for i, p := range pl {
		props[i] = graphstore.Prop{ID: p.ID, Name: p.Name, Value: p.Value}
	}
	return graphstore.Edge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: props}
}

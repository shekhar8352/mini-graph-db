// Package expr evaluates GQL-lite expressions and the built-in function library.
// It does not plan or run a query. The shell compiles a line and then calls exec.Run.
package expr

import (
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Env is the bindings an expression reads.
// Vars at the base frame are the row. Nested frames hide those names.
// A zero Now means now() reads the wall clock. A nil Rand means rand()
// draws from the global generator.
type Env struct {
	vars   []map[string]value.Value
	Params map[string]value.Value
	Graph  Graph
	Now    func() value.Value
	Rand   func() float64
}

// Set binds name in the row frame.
func (e *Env) Set(name string, v value.Value) {
	e.base()[name] = v
}

func (e *Env) base() map[string]value.Value {
	if len(e.vars) == 0 {
		e.vars = []map[string]value.Value{{}}
	}
	return e.vars[0]
}

func (e *Env) push() {
	if len(e.vars) == 0 {
		e.vars = []map[string]value.Value{{}}
	}
	e.vars = append(e.vars, map[string]value.Value{})
}

func (e *Env) pop() {
	e.vars = e.vars[:len(e.vars)-1]
}

func (e *Env) bind(name string, v value.Value) {
	e.vars[len(e.vars)-1][name] = v
}

func (e *Env) lookup(name string) (value.Value, bool) {
	for i := len(e.vars) - 1; i >= 0; i-- {
		if v, ok := e.vars[i][name]; ok {
			return v, true
		}
	}
	return value.Null(), false
}

// NodeRec is one node visible to graph functions.
type NodeRec struct {
	ID     uint64
	Labels []string
	Props  map[string]value.Value
}

// EdgeRec is one directed edge visible to graph functions.
type EdgeRec struct {
	ID    uint64
	From  uint64
	To    uint64
	Type  string
	Props map[string]value.Value
}

// Graph is the read view degree, neighbors, and path functions use.
type Graph interface {
	Node(id uint64) (NodeRec, bool)
	Edge(id uint64) (EdgeRec, bool)
	Out(id uint64) []EdgeRec
	In(id uint64) []EdgeRec
}

// Mem is an in-memory Graph.
type Mem struct {
	nodes map[uint64]NodeRec
	edges map[uint64]EdgeRec
	out   map[uint64][]EdgeRec
	in    map[uint64][]EdgeRec
}

// NewMem returns an empty graph view.
func NewMem() *Mem {
	return &Mem{
		nodes: map[uint64]NodeRec{},
		edges: map[uint64]EdgeRec{},
		out:   map[uint64][]EdgeRec{},
		in:    map[uint64][]EdgeRec{},
	}
}

// AddNode copies labels and properties into the view.
func (m *Mem) AddNode(id uint64, labels []string, props map[string]value.Value) {
	labs := append([]string(nil), labels...)
	sort.Strings(labs)
	w := 0
	for _, lab := range labs {
		if w > 0 && labs[w-1] == lab {
			continue
		}
		labs[w] = lab
		w++
	}
	cp := make(map[string]value.Value, len(props))
	for k, v := range props {
		cp[k] = v
	}
	m.nodes[id] = NodeRec{ID: id, Labels: labs[:w], Props: cp}
}

// AddEdge copies the edge and indexes it by endpoint.
func (m *Mem) AddEdge(id, from, to uint64, typ string, props map[string]value.Value) {
	cp := make(map[string]value.Value, len(props))
	for k, v := range props {
		cp[k] = v
	}
	e := EdgeRec{ID: id, From: from, To: to, Type: typ, Props: cp}
	m.edges[id] = e
	m.out[from] = append(m.out[from], e)
	m.in[to] = append(m.in[to], e)
	sort.Slice(m.out[from], func(i, j int) bool { return m.out[from][i].ID < m.out[from][j].ID })
	sort.Slice(m.in[to], func(i, j int) bool { return m.in[to][i].ID < m.in[to][j].ID })
}

// Node returns a node.
func (m *Mem) Node(id uint64) (NodeRec, bool) {
	n, ok := m.nodes[id]
	return n, ok
}

// Edge returns an edge.
func (m *Mem) Edge(id uint64) (EdgeRec, bool) {
	e, ok := m.edges[id]
	return e, ok
}

// Out returns outgoing edges sorted by id.
func (m *Mem) Out(id uint64) []EdgeRec {
	return append([]EdgeRec(nil), m.out[id]...)
}

// In returns incoming edges sorted by id.
func (m *Mem) In(id uint64) []EdgeRec {
	return append([]EdgeRec(nil), m.in[id]...)
}

// FromGraph copies the current nodes and edges of g.
func FromGraph(g *graph.Graph) Graph {
	m := NewMem()
	if g == nil {
		return m
	}
	for _, n := range g.AllNodes() {
		m.AddNode(n.ID, n.Labels(), n.Properties())
	}
	for _, e := range g.AllEdges() {
		m.AddEdge(e.ID, e.From, e.To, e.Label, e.Properties())
	}
	return m
}

// Eval evaluates x. Aggregates are not scalar; use NewAgg for those.
func Eval(x ast.Expr, env *Env) (value.Value, error) {
	if env == nil {
		return value.Null(), gerr.New(gerr.InvalidArgument, "missing environment")
	}
	env.base()
	return eval(x, env)
}

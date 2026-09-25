package graph

import (
	"fmt"
	"sort"

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

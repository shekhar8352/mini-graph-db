// Package graphstore implements node, edge, adjacency, and label operations
// on a storage.Tx. IDs and names live in the catalog keyspace.
package graphstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Prop is one property stored under an interned key id.
type Prop struct {
	ID    uint32
	Name  string
	Value value.Value
}

// Node is a vertex with sorted unique labels and typed properties.
type Node struct {
	ID     uint64
	Labels []string
	Props  []Prop
}

// Edge is a directed relationship with one type.
type Edge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	Props []Prop
}

// Stats is a snapshot of graph cardinality.
type Stats struct {
	Nodes  int
	Edges  int
	Labels []string
}

// Snapshot is a full copy of the graph, including id counters and the
// property-key table (index is the key id; entry 0 is unused).
type Snapshot struct {
	Nodes    []Node
	Edges    []Edge
	NextNode uint64
	NextEdge uint64
	PropKeys []string
}

type edgeRec struct {
	Edge
	TypeID uint32
}

var dataKeyspaces = []storage.Keyspace{
	storage.KSNode,
	storage.KSEdge,
	storage.KSOut,
	storage.KSIn,
	storage.KSLabel,
	storage.KSEdgeType,
	storage.KSProp,
	storage.KSCatalog,
}

// CreateNode allocates a node id and writes the node, its label index, and
// its property index.
func CreateNode(tx storage.Tx, labels []string, props map[string]value.Value) (Node, error) {
	labels = normalizeLabels(labels)
	ps, err := assignProps(tx, props)
	if err != nil {
		return Node{}, err
	}
	id, err := alloc(tx, catNextNode)
	if err != nil {
		return Node{}, err
	}
	n := Node{ID: id, Labels: labels, Props: ps}
	if err := writeNode(tx, n); err != nil {
		return Node{}, err
	}
	return n, nil
}

// GetNode reads a node. A missing id returns a not-found error.
func GetNode(tx storage.Tx, id uint64) (Node, error) {
	raw, err := tx.Get(storage.KSNode, idKey(id))
	if errors.Is(err, storage.ErrNotFound) {
		return Node{}, notFound("node", id)
	}
	if err != nil {
		return Node{}, err
	}
	dec, err := decodeNode(raw)
	if err != nil {
		return Node{}, err
	}
	labels, err := labelNames(tx, dec.LabelIDs)
	if err != nil {
		return Node{}, err
	}
	ps, err := fillPropNames(tx, dec.Props)
	if err != nil {
		return Node{}, err
	}
	return Node{ID: id, Labels: labels, Props: ps}, nil
}

// UpdateNode merges props into an existing node. An empty map is a no-op.
func UpdateNode(tx storage.Tx, id uint64, props map[string]value.Value) (Node, error) {
	n, err := GetNode(tx, id)
	if err != nil {
		return Node{}, err
	}
	if len(props) == 0 {
		return n, nil
	}
	if err := unindexProps(tx, id, n.Props); err != nil {
		return Node{}, err
	}
	byName := make(map[string]Prop, len(n.Props)+len(props))
	for _, p := range n.Props {
		byName[p.Name] = p
	}
	for name, v := range props {
		if !v.Storable() {
			return Node{}, invalidf("property %q is not storable", name)
		}
		pid, err := intern(tx, catProp, catPropRev, catNextProp, name)
		if err != nil {
			return Node{}, err
		}
		byName[name] = Prop{ID: pid, Name: name, Value: v}
	}
	n.Props = propsFromNames(byName)
	if err := writeNode(tx, n); err != nil {
		return Node{}, err
	}
	return n, nil
}

// DeleteNode removes a node and every incident edge.
func DeleteNode(tx storage.Tx, id uint64) error {
	n, err := GetNode(tx, id)
	if err != nil {
		return err
	}
	eids, err := incidentEdges(tx, id)
	if err != nil {
		return err
	}
	seen := make(map[uint64]struct{}, len(eids))
	for _, eid := range eids {
		if _, ok := seen[eid]; ok {
			continue
		}
		seen[eid] = struct{}{}
		if err := DeleteEdge(tx, eid); err != nil {
			return err
		}
	}
	if err := unindexLabels(tx, id, n.Labels); err != nil {
		return err
	}
	if err := unindexProps(tx, id, n.Props); err != nil {
		return err
	}
	return tx.Delete(storage.KSNode, idKey(id))
}

// CreateEdge allocates an edge id. Both endpoints must already exist.
func CreateEdge(tx storage.Tx, from, to uint64, label string, props map[string]value.Value) (Edge, error) {
	if _, err := GetNode(tx, from); err != nil {
		if IsNotFound(err) {
			return Edge{}, notFound("from node", from)
		}
		return Edge{}, err
	}
	if _, err := GetNode(tx, to); err != nil {
		if IsNotFound(err) {
			return Edge{}, notFound("to node", to)
		}
		return Edge{}, err
	}
	ps, err := assignProps(tx, props)
	if err != nil {
		return Edge{}, err
	}
	id, err := alloc(tx, catNextEdge)
	if err != nil {
		return Edge{}, err
	}
	typeID, err := intern(tx, catEdgeType, catTypeRev, catNextType, label)
	if err != nil {
		return Edge{}, err
	}
	e := edgeRec{Edge: Edge{ID: id, From: from, To: to, Label: label, Props: ps}, TypeID: typeID}
	if err := writeEdge(tx, e); err != nil {
		return Edge{}, err
	}
	return e.Edge, nil
}

// GetEdge reads an edge. A missing id returns a not-found error.
func GetEdge(tx storage.Tx, id uint64) (Edge, error) {
	rec, err := loadEdge(tx, id)
	if err != nil {
		return Edge{}, err
	}
	return rec.Edge, nil
}

// UpdateEdge merges props into an existing edge. An empty map is a no-op.
func UpdateEdge(tx storage.Tx, id uint64, props map[string]value.Value) (Edge, error) {
	rec, err := loadEdge(tx, id)
	if err != nil {
		return Edge{}, err
	}
	if len(props) == 0 {
		return rec.Edge, nil
	}
	byName := make(map[string]Prop, len(rec.Props)+len(props))
	for _, p := range rec.Props {
		byName[p.Name] = p
	}
	for name, v := range props {
		if !v.Storable() {
			return Edge{}, invalidf("property %q is not storable", name)
		}
		pid, err := intern(tx, catProp, catPropRev, catNextProp, name)
		if err != nil {
			return Edge{}, err
		}
		byName[name] = Prop{ID: pid, Name: name, Value: v}
	}
	rec.Props = propsFromNames(byName)
	if err := writeEdge(tx, rec); err != nil {
		return Edge{}, err
	}
	return rec.Edge, nil
}

// DeleteEdge removes an edge. Endpoints are left intact.
func DeleteEdge(tx storage.Tx, id uint64) error {
	rec, err := loadEdge(tx, id)
	if err != nil {
		return err
	}
	if err := tx.Delete(storage.KSOut, outKey(rec.From, rec.TypeID, rec.To, rec.ID)); err != nil {
		return err
	}
	if err := tx.Delete(storage.KSIn, inKey(rec.To, rec.TypeID, rec.From, rec.ID)); err != nil {
		return err
	}
	if err := tx.Delete(storage.KSEdgeType, typeKey(rec.TypeID, rec.ID)); err != nil {
		return err
	}
	return tx.Delete(storage.KSEdge, idKey(rec.ID))
}

// NodesByLabel returns every node that carries label, in id order.
func NodesByLabel(tx storage.Tx, label string) ([]Node, error) {
	id, ok, err := lookupName(tx, catLabel, label)
	if err != nil || !ok {
		return nil, err
	}
	keys, err := scanPrefix(tx, storage.KSLabel, labelKey(id, 0)[:4])
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, k := range keys {
		if len(k) != 12 {
			continue
		}
		n, err := GetNode(tx, binary.BigEndian.Uint64(k[4:]))
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// NodesByPropEq returns nodes whose property equals val, using the property index.
// Numeric equality follows EncodeKey, so int 1 matches float 1.0.
func NodesByPropEq(tx storage.Tx, key string, val value.Value) ([]Node, error) {
	id, ok, err := lookupName(tx, catProp, key)
	if err != nil || !ok {
		return nil, err
	}
	prefix := propIndexPrefix(id, val)
	keys, err := scanPrefix(tx, storage.KSProp, prefix)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, k := range keys {
		if len(k) != len(prefix)+8 || !bytes.Equal(k[:len(prefix)], prefix) {
			continue
		}
		n, err := GetNode(tx, binary.BigEndian.Uint64(k[len(prefix):]))
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// AllNodes returns every node in id order.
func AllNodes(tx storage.Tx) ([]Node, error) {
	keys, err := scanPrefix(tx, storage.KSNode, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(keys))
	for _, k := range keys {
		if len(k) != 8 {
			continue
		}
		n, err := GetNode(tx, binary.BigEndian.Uint64(k))
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// AllEdges returns every edge in id order.
func AllEdges(tx storage.Tx) ([]Edge, error) {
	keys, err := scanPrefix(tx, storage.KSEdge, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Edge, 0, len(keys))
	for _, k := range keys {
		if len(k) != 8 {
			continue
		}
		e, err := GetEdge(tx, binary.BigEndian.Uint64(k))
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// EdgesBetween returns directed edges from -> to. Both nodes must exist.
func EdgesBetween(tx storage.Tx, from, to uint64) ([]Edge, error) {
	if _, err := GetNode(tx, from); err != nil {
		if IsNotFound(err) {
			return nil, notFound("node", from)
		}
		return nil, err
	}
	if _, err := GetNode(tx, to); err != nil {
		if IsNotFound(err) {
			return nil, notFound("node", to)
		}
		return nil, err
	}
	keys, err := scanPrefix(tx, storage.KSOut, idKey(from))
	if err != nil {
		return nil, err
	}
	var out []Edge
	for _, k := range keys {
		_, _, dest, eid, ok := parseAdj(k)
		if !ok || dest != to {
			continue
		}
		e, err := GetEdge(tx, eid)
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// EdgesByLabel returns every edge of the given type, in id order.
func EdgesByLabel(tx storage.Tx, label string) ([]Edge, error) {
	id, ok, err := lookupName(tx, catEdgeType, label)
	if err != nil || !ok {
		return nil, err
	}
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, id)
	keys, err := scanPrefix(tx, storage.KSEdgeType, prefix)
	if err != nil {
		return nil, err
	}
	var out []Edge
	for _, k := range keys {
		if len(k) != 12 {
			continue
		}
		e, err := GetEdge(tx, binary.BigEndian.Uint64(k[4:]))
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GraphStats returns node and edge counts and the labels that still have nodes.
func GraphStats(tx storage.Tx) (Stats, error) {
	nodes, err := scanPrefix(tx, storage.KSNode, nil)
	if err != nil {
		return Stats{}, err
	}
	edges, err := scanPrefix(tx, storage.KSEdge, nil)
	if err != nil {
		return Stats{}, err
	}
	labelKeys, err := scanPrefix(tx, storage.KSLabel, nil)
	if err != nil {
		return Stats{}, err
	}
	seen := map[uint32]struct{}{}
	var labels []string
	for _, k := range labelKeys {
		if len(k) < 4 {
			continue
		}
		id := binary.BigEndian.Uint32(k[:4])
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name, ok, err := catName(tx, catLabelRev, id)
		if err != nil {
			return Stats{}, err
		}
		if ok {
			labels = append(labels, name)
		}
	}
	sort.Strings(labels)
	return Stats{Nodes: len(nodes), Edges: len(edges), Labels: labels}, nil
}

// Export copies the graph. PropKeys[0] is unused.
func Export(tx storage.Tx) (Snapshot, error) {
	nodes, err := AllNodes(tx)
	if err != nil {
		return Snapshot{}, err
	}
	edges, err := AllEdges(tx)
	if err != nil {
		return Snapshot{}, err
	}
	nextN, err := readCounter(tx, catNextNode)
	if err != nil {
		return Snapshot{}, err
	}
	nextE, err := readCounter(tx, catNextEdge)
	if err != nil {
		return Snapshot{}, err
	}
	keys, err := exportPropKeys(tx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Nodes:    nodes,
		Edges:    edges,
		NextNode: nextN,
		NextEdge: nextE,
		PropKeys: keys,
	}, nil
}

// Replace deletes every graph keyspace and writes snap in the same transaction.
func Replace(tx storage.Tx, snap Snapshot) error {
	for _, ks := range dataKeyspaces {
		if err := clearKeyspace(tx, ks); err != nil {
			return err
		}
	}
	keys := normalizePropKeys(snap.PropKeys)
	if err := installPropKeys(tx, keys); err != nil {
		return err
	}
	nextN := snap.NextNode
	if nextN == 0 {
		nextN = 1
	}
	nextE := snap.NextEdge
	if nextE == 0 {
		nextE = 1
	}
	for _, n := range snap.Nodes {
		if n.ID >= nextN {
			nextN = n.ID + 1
		}
		if err := insertNode(tx, n); err != nil {
			return err
		}
	}
	for _, e := range snap.Edges {
		if e.ID >= nextE {
			nextE = e.ID + 1
		}
		if err := insertEdge(tx, e); err != nil {
			return err
		}
	}
	if err := writeCounter(tx, catNextNode, nextN); err != nil {
		return err
	}
	return writeCounter(tx, catNextEdge, nextE)
}

// InternProp assigns or returns the id for a property name.
func InternProp(tx storage.Tx, name string) (uint32, error) {
	return intern(tx, catProp, catPropRev, catNextProp, name)
}

// PropName returns the property name for an interned id.
func PropName(tx storage.Tx, id uint32) (string, bool, error) {
	return catName(tx, catPropRev, id)
}

func writeNode(tx storage.Tx, n Node) error {
	lids, err := internLabels(tx, n.Labels)
	if err != nil {
		return err
	}
	if err := tx.Put(storage.KSNode, idKey(n.ID), encodeNode(lids, n.Props)); err != nil {
		return err
	}
	if err := indexLabels(tx, n.ID, lids); err != nil {
		return err
	}
	return indexProps(tx, n.ID, n.Props)
}

func writeEdge(tx storage.Tx, e edgeRec) error {
	if err := tx.Put(storage.KSEdge, idKey(e.ID), encodeEdge(e.From, e.To, e.TypeID, e.Props)); err != nil {
		return err
	}
	if err := tx.Put(storage.KSOut, outKey(e.From, e.TypeID, e.To, e.ID), nil); err != nil {
		return err
	}
	if err := tx.Put(storage.KSIn, inKey(e.To, e.TypeID, e.From, e.ID), nil); err != nil {
		return err
	}
	return tx.Put(storage.KSEdgeType, typeKey(e.TypeID, e.ID), nil)
}

func insertNode(tx storage.Tx, n Node) error {
	ps, err := bindProps(tx, n.Props)
	if err != nil {
		return err
	}
	n.Labels = normalizeLabels(n.Labels)
	n.Props = ps
	return writeNode(tx, n)
}

func insertEdge(tx storage.Tx, e Edge) error {
	ps, err := bindProps(tx, e.Props)
	if err != nil {
		return err
	}
	typeID, err := intern(tx, catEdgeType, catTypeRev, catNextType, e.Label)
	if err != nil {
		return err
	}
	e.Props = ps
	return writeEdge(tx, edgeRec{Edge: e, TypeID: typeID})
}

func loadEdge(tx storage.Tx, id uint64) (edgeRec, error) {
	raw, err := tx.Get(storage.KSEdge, idKey(id))
	if errors.Is(err, storage.ErrNotFound) {
		return edgeRec{}, notFound("edge", id)
	}
	if err != nil {
		return edgeRec{}, err
	}
	dec, err := decodeEdge(raw)
	if err != nil {
		return edgeRec{}, err
	}
	name, ok, err := catName(tx, catTypeRev, dec.TypeID)
	if err != nil {
		return edgeRec{}, err
	}
	if !ok {
		return edgeRec{}, corrupt("edge type")
	}
	ps, err := fillPropNames(tx, dec.Props)
	if err != nil {
		return edgeRec{}, err
	}
	return edgeRec{
		Edge:   Edge{ID: id, From: dec.From, To: dec.To, Label: name, Props: ps},
		TypeID: dec.TypeID,
	}, nil
}

func incidentEdges(tx storage.Tx, nodeID uint64) ([]uint64, error) {
	var ids []uint64
	for _, ks := range []storage.Keyspace{storage.KSOut, storage.KSIn} {
		keys, err := scanPrefix(tx, ks, idKey(nodeID))
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			_, _, _, eid, ok := parseAdj(k)
			if ok {
				ids = append(ids, eid)
			}
		}
	}
	return ids, nil
}

func assignProps(tx storage.Tx, props map[string]value.Value) ([]Prop, error) {
	byID := make(map[uint32]Prop, len(props))
	for name, v := range props {
		if !v.Storable() {
			return nil, invalidf("property %q is not storable", name)
		}
		id, err := intern(tx, catProp, catPropRev, catNextProp, name)
		if err != nil {
			return nil, err
		}
		byID[id] = Prop{ID: id, Name: name, Value: v}
	}
	return propsByID(byID), nil
}

func bindProps(tx storage.Tx, props []Prop) ([]Prop, error) {
	byID := make(map[uint32]Prop, len(props))
	for _, p := range props {
		if !p.Value.Storable() {
			return nil, invalidf("property %q is not storable", p.Name)
		}
		id := p.ID
		name := p.Name
		known, ok, err := catName(tx, catPropRev, id)
		if err != nil {
			return nil, err
		}
		if ok {
			if name == "" {
				name = known
			}
		} else {
			id, err = intern(tx, catProp, catPropRev, catNextProp, name)
			if err != nil {
				return nil, err
			}
		}
		byID[id] = Prop{ID: id, Name: name, Value: p.Value}
	}
	return propsByID(byID), nil
}

func propsByID(byID map[uint32]Prop) []Prop {
	out := make([]Prop, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func propsFromNames(byName map[string]Prop) []Prop {
	out := make([]Prop, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func internLabels(tx storage.Tx, labels []string) ([]uint32, error) {
	out := make([]uint32, 0, len(labels))
	for _, l := range labels {
		id, err := intern(tx, catLabel, catLabelRev, catNextLabel, l)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func labelNames(tx storage.Tx, ids []uint32) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		name, ok, err := catName(tx, catLabelRev, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, corrupt("label")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func fillPropNames(tx storage.Tx, props []Prop) ([]Prop, error) {
	for i, p := range props {
		name, ok, err := catName(tx, catPropRev, p.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, corrupt("property key")
		}
		props[i].Name = name
	}
	return props, nil
}

func indexLabels(tx storage.Tx, nodeID uint64, labelIDs []uint32) error {
	seen := map[uint32]struct{}{}
	for _, id := range labelIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := tx.Put(storage.KSLabel, labelKey(id, nodeID), nil); err != nil {
			return err
		}
	}
	return nil
}

func unindexLabels(tx storage.Tx, nodeID uint64, labels []string) error {
	for _, l := range labels {
		id, ok, err := lookupName(tx, catLabel, l)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := tx.Delete(storage.KSLabel, labelKey(id, nodeID)); err != nil {
			return err
		}
	}
	return nil
}

func indexProps(tx storage.Tx, nodeID uint64, props []Prop) error {
	for _, p := range props {
		if err := tx.Put(storage.KSProp, propIndexKey(p.ID, p.Value, nodeID), nil); err != nil {
			return err
		}
	}
	return nil
}

func unindexProps(tx storage.Tx, nodeID uint64, props []Prop) error {
	for _, p := range props {
		if err := tx.Delete(storage.KSProp, propIndexKey(p.ID, p.Value, nodeID)); err != nil {
			return err
		}
	}
	return nil
}

func lookupName(tx storage.Tx, kind byte, name string) (uint32, bool, error) {
	raw, err := tx.Get(storage.KSCatalog, catalogKey(kind, name))
	if errors.Is(err, storage.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(raw) != 4 {
		return 0, false, corrupt("catalog id")
	}
	return binary.BigEndian.Uint32(raw), true, nil
}

func installPropKeys(tx storage.Tx, keys []string) error {
	var maxID uint32
	for id, name := range keys {
		if id == 0 || name == "" {
			continue
		}
		if err := putName(tx, catProp, catPropRev, name, uint32(id)); err != nil {
			return err
		}
		if uint32(id) > maxID {
			maxID = uint32(id)
		}
	}
	next := uint64(len(keys))
	if uint64(maxID)+1 > next {
		next = uint64(maxID) + 1
	}
	if next == 0 {
		next = 1
	}
	return writeCounter(tx, catNextProp, next)
}

func exportPropKeys(tx storage.Tx) ([]string, error) {
	keys, err := scanPrefix(tx, storage.KSCatalog, []byte{catPropRev})
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return []string{""}, nil
	}
	names := map[uint32]string{}
	var maxID uint32
	for _, k := range keys {
		if len(k) != 5 || k[0] != catPropRev {
			continue
		}
		id := binary.BigEndian.Uint32(k[1:])
		raw, err := tx.Get(storage.KSCatalog, k)
		if err != nil {
			return nil, err
		}
		names[id] = string(raw)
		if id > maxID {
			maxID = id
		}
	}
	out := make([]string, maxID+1)
	for id, name := range names {
		out[id] = name
	}
	return out, nil
}

func normalizePropKeys(keys []string) []string {
	if len(keys) == 0 {
		return []string{""}
	}
	out := append([]string(nil), keys...)
	if out[0] != "" {
		out = append([]string{""}, out...)
	}
	return out
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

func clearKeyspace(tx storage.Tx, ks storage.Keyspace) error {
	keys, err := scanPrefix(tx, ks, nil)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := tx.Delete(ks, k); err != nil {
			return err
		}
	}
	return nil
}

func scanPrefix(tx storage.Tx, ks storage.Keyspace, prefix []byte) ([][]byte, error) {
	cur, err := tx.Cursor(ks)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close() }()
	var out [][]byte
	if !cur.Seek(prefix) {
		return nil, nil
	}
	for {
		k := cur.Key()
		if prefix != nil && !bytes.HasPrefix(k, prefix) {
			break
		}
		out = append(out, k)
		if !cur.Next() {
			break
		}
	}
	return out, nil
}

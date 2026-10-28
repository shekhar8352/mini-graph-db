package graph

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/index"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Schema returns the current catalog snapshot.
func (g *Graph) Schema() (*catalog.Schema, error) {
	var sch *catalog.Schema
	err := g.read(func(tx storage.Tx) error {
		gen, err := catalog.Generation(tx)
		if err != nil {
			return err
		}
		if g.schema != nil && g.schema.Generation == gen {
			sch = g.schema
			return nil
		}
		sch, err = loadSchema(tx)
		if err != nil {
			return err
		}
		g.schema = sch
		return nil
	})
	return sch, mapErr(err)
}

func loadSchema(tx storage.Tx) (*catalog.Schema, error) {
	sch, err := catalog.Load(tx)
	if err != nil {
		return nil, err
	}
	uniques, err := graphstore.ListUniques(tx)
	if err != nil {
		return nil, err
	}
	have := map[string]struct{}{}
	for _, c := range sch.Constraints {
		have[c.Name] = struct{}{}
	}
	for _, u := range uniques {
		if _, ok := have[u.Name]; ok {
			continue
		}
		sch.Constraints = append(sch.Constraints, catalog.Constraint{
			Name: u.Name, Kind: catalog.UniqueConstraint, Entity: catalog.NodeEntity,
			Label: u.Label, Prop: u.Prop, IndexName: u.Name,
		})
	}
	return sch, nil
}

// CreateIndex builds a declared index in this transaction and commits it ONLINE.
func (g *Graph) CreateIndex(name string, edge bool, label string, props []string, unique bool) error {
	if name == "" || label == "" || len(props) == 0 {
		return gerr.New(gerr.InvalidArgument, "index needs a name, label, and property")
	}
	return mapErr(g.write(func(tx storage.Tx) error {
		return createIndexTx(tx, name, edge, label, props, unique)
	}))
}

func createIndexTx(tx storage.Tx, name string, edge bool, label string, props []string, unique bool) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return err
	}
	if _, ok := sch.IndexByName(name); ok {
		return gerr.New(gerr.AlreadyExists, "index "+name)
	}
	id, err := catalog.AllocIndexID(tx)
	if err != nil {
		return err
	}
	ix := catalog.Index{
		ID: id, Name: name, Label: label, Props: append([]string(nil), props...),
		Unique: unique, State: catalog.Online, Entity: catalog.NodeEntity,
	}
	if edge {
		ix.Entity = catalog.EdgeEntity
	}
	if err := catalog.PutIndex(tx, ix); err != nil {
		return err
	}
	if err := buildIndex(tx, ix); err != nil {
		return err
	}
	return nil
}

func buildIndex(tx storage.Tx, ix catalog.Index) error {
	if ix.Entity == catalog.EdgeEntity {
		edges, err := graphstore.AllEdges(tx)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if ix.Label != "" && e.Label != ix.Label {
				continue
			}
			vals, ok := valuesOf(ix.Props, e.Props)
			if !ok {
				continue
			}
			if err := index.Put(tx, ix.ID, vals, e.ID); err != nil {
				return err
			}
		}
		return nil
	}
	var nodes []graphstore.Node
	var err error
	if ix.Label != "" {
		nodes, err = graphstore.NodesByLabel(tx, ix.Label)
	} else {
		nodes, err = graphstore.AllNodes(tx)
	}
	if err != nil {
		return err
	}
	for _, n := range nodes {
		vals, ok := valuesOf(ix.Props, n.Props)
		if !ok {
			continue
		}
		if err := index.Put(tx, ix.ID, vals, n.ID); err != nil {
			return err
		}
	}
	return nil
}

func valuesOf(names []string, props []graphstore.Prop) ([]value.Value, bool) {
	m := map[string]value.Value{}
	for _, p := range props {
		m[p.Name] = p.Value
	}
	vals := make([]value.Value, len(names))
	for i, name := range names {
		v, ok := m[name]
		if !ok || v.Kind() == value.KindNull || !v.Storable() {
			return nil, false
		}
		vals[i] = v
	}
	return vals, true
}

// DropIndex removes a declared index. A missing name is NotFound.
func (g *Graph) DropIndex(name string) error {
	return mapErr(g.write(func(tx storage.Tx) error {
		return dropIndexTx(tx, name)
	}))
}

func dropIndexTx(tx storage.Tx, name string) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return err
	}
	ix, ok := sch.IndexByName(name)
	if !ok {
		return gerr.New(gerr.NotFound, "index "+name)
	}
	if err := index.Drop(tx, ix.ID); err != nil {
		return err
	}
	ok, err = catalog.DropIndex(tx, name)
	if err != nil {
		return err
	}
	if !ok {
		return gerr.New(gerr.NotFound, "index "+name)
	}
	return nil
}

// CreateConstraint stores a UNIQUE, EXISTS, or TYPE constraint.
func (g *Graph) CreateConstraint(name string, edge bool, label, prop string, kind catalog.ConstraintKind, typeName string) error {
	if name == "" || label == "" || prop == "" {
		return gerr.New(gerr.InvalidArgument, "constraint needs a name, label, and property")
	}
	return mapErr(g.write(func(tx storage.Tx) error {
		return createConstraintTx(tx, name, edge, label, prop, kind, typeName)
	}))
}

func createConstraintTx(tx storage.Tx, name string, edge bool, label, prop string, kind catalog.ConstraintKind, typeName string) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return err
	}
	if _, ok := sch.ConstraintByName(name); ok {
		return gerr.New(gerr.AlreadyExists, "constraint "+name)
	}
	uniques, err := graphstore.ListUniques(tx)
	if err != nil {
		return err
	}
	for _, u := range uniques {
		if u.Name == name {
			return gerr.New(gerr.AlreadyExists, "constraint "+name)
		}
	}
	entity := catalog.NodeEntity
	if edge {
		entity = catalog.EdgeEntity
	}
	c := catalog.Constraint{Name: name, Kind: kind, Entity: entity, Label: label, Prop: prop, TypeName: typeName}
	if kind == catalog.UniqueConstraint {
		if _, ok := sch.IndexByName(name); !ok {
			if err := createIndexTx(tx, name, edge, label, []string{prop}, true); err != nil {
				return err
			}
			c.IndexName = name
		}
		if !edge {
			if err := graphstore.PutUnique(tx, name, label, prop); err != nil {
				return err
			}
		}
	}
	if err := catalog.PutConstraint(tx, c); err != nil {
		return err
	}
	return checkExisting(tx, c)
}

func checkExisting(tx storage.Tx, c catalog.Constraint) error {
	if c.Kind == catalog.UniqueConstraint && c.Entity == catalog.NodeEntity {
		return nil
	}
	return checkOne(tx, c)
}

// DropConstraint removes a constraint. A missing name is NotFound.
func (g *Graph) DropConstraint(name string) error {
	return mapErr(g.write(func(tx storage.Tx) error {
		return dropConstraintTx(tx, name)
	}))
}

func dropConstraintTx(tx storage.Tx, name string) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return err
	}
	c, ok := sch.ConstraintByName(name)
	legacy := false
	if !ok {
		uniques, err := graphstore.ListUniques(tx)
		if err != nil {
			return err
		}
		for _, u := range uniques {
			if u.Name == name {
				ok = true
				c = catalog.Constraint{Name: u.Name, Kind: catalog.UniqueConstraint, Entity: catalog.NodeEntity, Label: u.Label, Prop: u.Prop, IndexName: u.Name}
				legacy = true
				break
			}
		}
	}
	if !ok {
		return gerr.New(gerr.NotFound, "constraint "+name)
	}
	if c.Kind == catalog.UniqueConstraint && c.Entity == catalog.NodeEntity {
		if _, err := graphstore.DropUnique(tx, name); err != nil {
			return err
		}
	}
	if c.IndexName == name {
		if _, found := sch.IndexByName(name); found {
			if err := dropIndexTx(tx, name); err != nil {
				return err
			}
		}
	}
	if !legacy {
		if _, err := catalog.DropConstraint(tx, name); err != nil {
			return err
		}
	}
	return nil
}

// IndexEqual looks up a declared ONLINE index by equality.
func (g *Graph) IndexEqual(name string, vals []value.Value) ([]uint64, error) {
	return g.indexSeek(name, func(tx storage.Tx, ix catalog.Index) ([]uint64, error) {
		return index.Equal(tx, ix.ID, vals)
	})
}

// IndexPrefix looks up a declared ONLINE index by a leading composite.
func (g *Graph) IndexPrefix(name string, vals []value.Value) ([]uint64, error) {
	return g.indexSeek(name, func(tx storage.Tx, ix catalog.Index) ([]uint64, error) {
		return index.PrefixScan(tx, ix.ID, vals)
	})
}

// IndexRange looks up a declared ONLINE index by a range on the encoded prefix.
func (g *Graph) IndexRange(name string, lo, hi []value.Value, loInc, hiInc bool) ([]uint64, error) {
	return g.indexSeek(name, func(tx storage.Tx, ix catalog.Index) ([]uint64, error) {
		return index.Range(tx, ix.ID, lo, hi, loInc, hiInc)
	})
}

// IndexStartsWith looks up a declared ONLINE index by a string prefix.
func (g *Graph) IndexStartsWith(name string, s string) ([]uint64, error) {
	return g.indexSeek(name, func(tx storage.Tx, ix catalog.Index) ([]uint64, error) {
		return index.StartsWith(tx, ix.ID, s)
	})
}

func (g *Graph) indexSeek(name string, fn func(storage.Tx, catalog.Index) ([]uint64, error)) ([]uint64, error) {
	var ids []uint64
	err := g.read(func(tx storage.Tx) error {
		sch, err := catalog.Load(tx)
		if err != nil {
			return err
		}
		ix, ok := sch.IndexByName(name)
		if !ok || ix.State != catalog.Online {
			return nil
		}
		ids, err = fn(tx, ix)
		return err
	})
	return ids, mapErr(err)
}

func (g *Graph) prepareConstraints(tx storage.Tx) error {
	if err := g.prepareUniques(tx); err != nil {
		return err
	}
	if err := g.prepareExistsAndTypes(tx); err != nil {
		return err
	}
	return g.prepareUniqueLocks(tx)
}

func (g *Graph) prepareExistsAndTypes(tx storage.Tx) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return mapErr(err)
	}
	for _, c := range sch.Constraints {
		if c.Kind != catalog.ExistsConstraint && c.Kind != catalog.TypeConstraint {
			continue
		}
		if err := checkOne(tx, c); err != nil {
			return err
		}
	}
	return nil
}

func checkOne(tx storage.Tx, c catalog.Constraint) error {
	if c.Entity == catalog.EdgeEntity {
		edges, err := graphstore.AllEdges(tx)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if c.Label != "" && e.Label != c.Label {
				continue
			}
			if err := checkProps(c, e.Props); err != nil {
				return err
			}
		}
		return nil
	}
	nodes, err := graphstore.AllNodes(tx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if c.Label != "" && !hasLabel(n.Labels, c.Label) {
			continue
		}
		if err := checkProps(c, n.Props); err != nil {
			return err
		}
	}
	return nil
}

func checkProps(c catalog.Constraint, props []graphstore.Prop) error {
	v, ok := propOf(props, c.Prop)
	switch c.Kind {
	case catalog.ExistsConstraint:
		if !ok || v.Kind() == value.KindNull {
			return gerr.New(gerr.ConstraintViolation, "missing "+c.Prop)
		}
	case catalog.TypeConstraint:
		if !ok || v.Kind() == value.KindNull {
			return nil
		}
		if !typeOK(v, c.TypeName) {
			return gerr.New(gerr.ConstraintViolation, c.Prop+" is not "+c.TypeName)
		}
	}
	return nil
}

func propOf(props []graphstore.Prop, name string) (value.Value, bool) {
	for _, p := range props {
		if p.Name == name {
			return p.Value, true
		}
	}
	return value.Value{}, false
}

func typeOK(v value.Value, name string) bool {
	switch strings.ToUpper(name) {
	case "BOOL":
		return v.Kind() == value.KindBool
	case "INT":
		return v.Kind() == value.KindInt
	case "FLOAT":
		return v.Kind() == value.KindFloat
	case "STRING":
		return v.Kind() == value.KindString
	case "BYTES":
		return v.Kind() == value.KindBytes
	case "DATE":
		return v.Kind() == value.KindDate
	case "DATETIME":
		return v.Kind() == value.KindDateTime
	case "DURATION":
		return v.Kind() == value.KindDuration
	case "LIST":
		return v.Kind() == value.KindList
	case "MAP":
		return v.Kind() == value.KindMap
	case "NODE":
		return v.Kind() == value.KindNode
	case "EDGE":
		return v.Kind() == value.KindEdge
	case "PATH":
		return v.Kind() == value.KindPath
	default:
		return false
	}
}

func (g *Graph) prepareUniqueLocks(tx storage.Tx) error {
	sch, err := catalog.Load(tx)
	if err != nil {
		return mapErr(err)
	}
	desired := map[string]uint64{}
	for _, ix := range sch.Indexes {
		if !ix.Unique || ix.State != catalog.Online {
			continue
		}
		if err := collectUnique(tx, ix, desired); err != nil {
			return err
		}
	}
	have, err := graphstore.ScanCatalog(tx, catalog.UniqueLockPrefix())
	if err != nil {
		return mapErr(err)
	}
	for _, k := range have {
		raw, err := tx.Get(storage.KSCatalog, k)
		if errors.Is(err, storage.ErrNotFound) {
			raw, err = nil, nil
		}
		if err != nil {
			return mapErr(err)
		}
		want, claimed := desired[string(k)]
		id := uint64(0)
		if len(raw) == 8 {
			id = binary.BigEndian.Uint64(raw)
		}
		if !claimed || id != want {
			if err := tx.Delete(storage.KSCatalog, k); err != nil {
				return mapErr(err)
			}
			continue
		}
		delete(desired, string(k))
	}
	for k, id := range desired {
		if err := writeUnique(tx, []byte(k), id); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func collectUnique(tx storage.Tx, ix catalog.Index, desired map[string]uint64) error {
	if ix.Entity == catalog.EdgeEntity {
		edges, err := graphstore.AllEdges(tx)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if ix.Label != "" && e.Label != ix.Label {
				continue
			}
			vals, ok := valuesOf(ix.Props, e.Props)
			if !ok {
				continue
			}
			if err := claim(desired, catalog.UniqueLockKey(ix.ID, vals), e.ID); err != nil {
				return err
			}
		}
		return nil
	}
	nodes, err := graphstore.AllNodes(tx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if ix.Label != "" && !hasLabel(n.Labels, ix.Label) {
			continue
		}
		vals, ok := valuesOf(ix.Props, n.Props)
		if !ok {
			continue
		}
		if err := claim(desired, catalog.UniqueLockKey(ix.ID, vals), n.ID); err != nil {
			return err
		}
	}
	return nil
}

func claim(desired map[string]uint64, key []byte, id uint64) error {
	k := string(key)
	if prev, ok := desired[k]; ok && prev != id {
		return gerr.New(gerr.ConstraintViolation, "unique key already exists")
	}
	desired[k] = id
	return nil
}

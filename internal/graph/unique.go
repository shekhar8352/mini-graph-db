package graph

import (
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// DeclareUnique records a node UNIQUE constraint in the current transaction.
func (g *Graph) DeclareUnique(name, label, prop string) error {
	if name == "" || label == "" || prop == "" {
		return gerr.New(gerr.InvalidArgument, "constraint needs a name, label, and property")
	}
	err := g.write(func(tx storage.Tx) error {
		defs, err := graphstore.ListUniques(tx)
		if err != nil {
			return err
		}
		for _, d := range defs {
			if d.Name == name {
				return gerr.New(gerr.AlreadyExists, "constraint "+name)
			}
		}
		return graphstore.PutUnique(tx, name, label, prop)
	})
	return mapErr(err)
}

// DropUnique removes a stored UNIQUE constraint.
// It reports whether a constraint of that name was stored.
// A missing name leaves the catalog unchanged.
func (g *Graph) DropUnique(name string) (bool, error) {
	var removed bool
	err := g.write(func(tx storage.Tx) error {
		var err error
		removed, err = graphstore.DropUnique(tx, name)
		return err
	})
	return removed, mapErr(err)
}

// prepareUniques rejects two live values of a UNIQUE property and writes the
// index key that Commit validates. The transaction stays open on error.
func (g *Graph) prepareUniques(tx storage.Tx) error {
	defs, err := graphstore.ListUniques(tx)
	if err != nil || len(defs) == 0 {
		return mapErr(err)
	}
	nodes, err := graphstore.AllNodes(tx)
	if err != nil {
		return mapErr(err)
	}
	desired := map[string]uint64{}
	for _, n := range nodes {
		for _, d := range defs {
			if !hasLabel(n.Labels, d.Label) {
				continue
			}
			v, ok := propValue(n, d.Prop)
			if !ok || v.Kind() == value.KindNull {
				continue
			}
			k := string(graphstore.UniqueKey(d.Label, d.Prop, v))
			if prev, exists := desired[k]; exists && prev != n.ID {
				return gerr.New(gerr.ConstraintViolation, "unique key already exists")
			}
			desired[k] = n.ID
		}
	}
	have, err := graphstore.UniqueIndexKeys(tx)
	if err != nil {
		return mapErr(err)
	}
	for _, k := range have {
		id, ok, err := graphstore.UniqueOwner(tx, k)
		if err != nil {
			return mapErr(err)
		}
		want, claimed := desired[string(k)]
		if !claimed || !ok || id != want {
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

func writeUnique(tx storage.Tx, key []byte, id uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], id)
	type deferUnique interface {
		DeferUnique(storage.Keyspace, []byte, []byte) error
	}
	if u, ok := tx.(deferUnique); ok {
		return u.DeferUnique(storage.KSCatalog, key, buf[:])
	}
	return tx.Put(storage.KSCatalog, key, buf[:])
}

func hasLabel(labels []string, label string) bool {
	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

func propValue(n graphstore.Node, name string) (value.Value, bool) {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value, true
		}
	}
	return value.Value{}, false
}

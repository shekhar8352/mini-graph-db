package graphstore

import (
	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/index"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func indexDeclaredNode(tx storage.Tx, n Node) error {
	return touchDeclared(tx, catalog.NodeEntity, n.Labels, "", n.Props, n.ID, true)
}

func unindexDeclaredNode(tx storage.Tx, n Node) error {
	return touchDeclared(tx, catalog.NodeEntity, n.Labels, "", n.Props, n.ID, false)
}

func indexDeclaredEdge(tx storage.Tx, e Edge) error {
	return touchDeclared(tx, catalog.EdgeEntity, nil, e.Label, e.Props, e.ID, true)
}

func unindexDeclaredEdge(tx storage.Tx, e Edge) error {
	return touchDeclared(tx, catalog.EdgeEntity, nil, e.Label, e.Props, e.ID, false)
}

func touchDeclared(tx storage.Tx, entity catalog.Entity, labels []string, edgeType string, props []Prop, id uint64, add bool) error {
	sch, err := catalog.Load(tx)
	if err != nil || sch == nil || len(sch.Indexes) == 0 {
		return err
	}
	pm := propMap(props)
	for _, ix := range sch.Indexes {
		if ix.State != catalog.Online && ix.State != catalog.Populating {
			continue
		}
		if ix.Entity != entity {
			continue
		}
		if entity == catalog.NodeEntity {
			if ix.Label != "" && !hasLabel(labels, ix.Label) {
				continue
			}
		} else if ix.Label != "" && ix.Label != edgeType {
			continue
		}
		vals, ok := indexValues(ix.Props, pm)
		if !ok {
			continue
		}
		if add {
			if err := index.Put(tx, ix.ID, vals, id); err != nil {
				return err
			}
			continue
		}
		if err := index.Delete(tx, ix.ID, vals, id); err != nil {
			return err
		}
	}
	return nil
}

func bumpWrites(tx storage.Tx) error {
	return catalog.AddWrites(tx, 1)
}

func propMap(ps []Prop) map[string]value.Value {
	m := make(map[string]value.Value, len(ps))
	for _, p := range ps {
		m[p.Name] = p.Value
	}
	return m
}

func indexValues(names []string, m map[string]value.Value) ([]value.Value, bool) {
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

func hasLabel(labels []string, label string) bool {
	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

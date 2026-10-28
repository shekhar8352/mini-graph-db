package graph

import (
	"bytes"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/graphstore"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Analyze refreshes planner statistics.
func (g *Graph) Analyze() error {
	return mapErr(g.write(func(tx storage.Tx) error {
		return analyzeTx(tx)
	}))
}

func analyzeTx(tx storage.Tx) error {
	nodes, err := graphstore.AllNodes(tx)
	if err != nil {
		return err
	}
	edges, err := graphstore.AllEdges(tx)
	if err != nil {
		return err
	}
	st := catalog.Stats{
		Nodes:     int64(len(nodes)),
		Edges:     int64(len(edges)),
		Labels:    map[string]int64{},
		EdgeTypes: map[string]int64{},
	}
	acc := map[string]*histAcc{}
	for _, n := range nodes {
		seen := map[string]struct{}{}
		for _, l := range n.Labels {
			if _, ok := seen[l]; ok {
				continue
			}
			seen[l] = struct{}{}
			st.Labels[l]++
			for _, p := range n.Props {
				addHist(acc, catalog.PropKey(true, l, p.Name), p.Value)
			}
		}
	}
	for _, e := range edges {
		st.EdgeTypes[e.Label]++
		for _, p := range e.Props {
			addHist(acc, catalog.PropKey(false, e.Label, p.Name), p.Value)
		}
	}
	st.Props = map[string]catalog.Histogram{}
	for k, a := range acc {
		st.Props[k] = a.finish()
	}
	return catalog.PutStats(tx, st)
}

func maybeAnalyze(tx storage.Tx) error {
	n, err := catalog.WriteCount(tx)
	if err != nil {
		return err
	}
	if n < catalog.AnalyzeAfter {
		return nil
	}
	return analyzeTx(tx)
}

type histAcc struct {
	nulls int64
	keys  [][]byte
}

func addHist(acc map[string]*histAcc, key string, v value.Value) {
	h := acc[key]
	if h == nil {
		h = &histAcc{}
		acc[key] = h
	}
	if v.Kind() == value.KindNull || !v.Storable() {
		h.nulls++
		return
	}
	h.keys = append(h.keys, value.EncodeKey(v))
}

func (h *histAcc) finish() catalog.Histogram {
	sort.Slice(h.keys, func(i, j int) bool { return bytes.Compare(h.keys[i], h.keys[j]) < 0 })
	out := catalog.Histogram{Count: int64(len(h.keys)), Nulls: h.nulls, Buckets: make([]catalog.Bucket, 0, catalog.HistBuckets)}
	if len(h.keys) == 0 {
		return out
	}
	out.Min = append([]byte(nil), h.keys[0]...)
	out.Max = append([]byte(nil), h.keys[len(h.keys)-1]...)
	out.NDV = distinct(h.keys)
	n := len(h.keys)
	for i := 0; i < catalog.HistBuckets; i++ {
		lo := i * n / catalog.HistBuckets
		hi := (i + 1) * n / catalog.HistBuckets
		if lo >= hi {
			out.Buckets = append(out.Buckets, catalog.Bucket{})
			continue
		}
		part := h.keys[lo:hi]
		out.Buckets = append(out.Buckets, catalog.Bucket{
			Upper: append([]byte(nil), part[len(part)-1]...),
			Count: int64(len(part)),
			NDV:   distinct(part),
		})
	}
	return out
}

func distinct(keys [][]byte) int64 {
	if len(keys) == 0 {
		return 0
	}
	n := int64(1)
	for i := 1; i < len(keys); i++ {
		if !bytes.Equal(keys[i], keys[i-1]) {
			n++
		}
	}
	return n
}

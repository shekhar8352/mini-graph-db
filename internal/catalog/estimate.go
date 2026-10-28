package catalog

import "bytes"

// EstimateLabel is the expected cardinality of a label scan.
func (s *Schema) EstimateLabel(label string) float64 {
	if s == nil {
		return 1000
	}
	if s.Stats.Labels != nil {
		if n, ok := s.Stats.Labels[label]; ok {
			return float64(n)
		}
	}
	if s.Stats.Nodes > 0 {
		d := float64(len(s.Stats.Labels))
		if d < 1 {
			d = 1
		}
		return float64(s.Stats.Nodes) / d
	}
	return 1000
}

// EstimateAll is the expected cardinality of a full node scan.
func (s *Schema) EstimateAll() float64 {
	if s == nil || s.Stats.Nodes == 0 {
		return 1000
	}
	return float64(s.Stats.Nodes)
}

// EstimateEq is the expected cardinality of an equality on label.prop.
func (s *Schema) EstimateEq(label, prop string) float64 {
	h, ok := s.hist(true, label, prop)
	if !ok || h.NDV == 0 {
		n := s.EstimateLabel(label)
		if n < 10 {
			return 1
		}
		return n / 10
	}
	return float64(h.Count) / float64(h.NDV)
}

// EstimateRange is the expected cardinality of a closed range on encoded keys.
func (s *Schema) EstimateRange(label, prop string, lo, hi []byte) float64 {
	h, ok := s.hist(true, label, prop)
	if !ok || h.Count == 0 {
		return s.EstimateLabel(label) / 3
	}
	if len(h.Buckets) == 0 {
		return float64(h.Count) / 3
	}
	var n int64
	for _, b := range h.Buckets {
		if len(lo) > 0 && bytes.Compare(b.Upper, lo) < 0 {
			continue
		}
		if len(hi) > 0 && bytes.Compare(b.Upper, hi) > 0 {
			continue
		}
		n += b.Count
	}
	if n == 0 {
		return 1
	}
	return float64(n)
}

func (s *Schema) hist(node bool, label, prop string) (Histogram, bool) {
	if s == nil || s.Stats.Props == nil {
		return Histogram{}, false
	}
	h, ok := s.Stats.Props[PropKey(node, label, prop)]
	return h, ok
}

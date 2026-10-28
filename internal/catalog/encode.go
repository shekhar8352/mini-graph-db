package catalog

import (
	"encoding/binary"
	"errors"
	"sort"
)

func encodeIndex(ix Index) []byte {
	buf := []byte{1, 0, 0, 0, 0, byte(ix.Entity), byte(ix.State), 0}
	binary.BigEndian.PutUint32(buf[1:5], ix.ID)
	if ix.Unique {
		buf[7] = 1
	}
	buf = appendString(buf, ix.Label)
	buf = binary.AppendUvarint(buf, uint64(len(ix.Props)))
	for _, p := range ix.Props {
		buf = appendString(buf, p)
	}
	return buf
}

func decodeIndex(name string, raw []byte) (Index, error) {
	if len(raw) < 8 || raw[0] != 1 {
		return Index{}, errors.New("index record")
	}
	ix := Index{
		ID:     binary.BigEndian.Uint32(raw[1:5]),
		Name:   name,
		Entity: Entity(raw[5]),
		State:  State(raw[6]),
		Unique: raw[7] == 1,
	}
	rest := raw[8:]
	var err error
	ix.Label, rest, err = takeString(rest)
	if err != nil {
		return Index{}, err
	}
	n, r := binary.Uvarint(rest)
	if r <= 0 {
		return Index{}, errors.New("index record")
	}
	rest = rest[r:]
	for i := uint64(0); i < n; i++ {
		p, next, err := takeString(rest)
		if err != nil {
			return Index{}, err
		}
		ix.Props = append(ix.Props, p)
		rest = next
	}
	return ix, nil
}

func encodeConstraint(c Constraint) []byte {
	buf := []byte{1, byte(c.Kind), byte(c.Entity)}
	buf = appendString(buf, c.Label)
	buf = appendString(buf, c.Prop)
	buf = appendString(buf, c.TypeName)
	buf = appendString(buf, c.IndexName)
	return buf
}

func decodeConstraint(name string, raw []byte) (Constraint, error) {
	if len(raw) < 3 || raw[0] != 1 {
		return Constraint{}, errors.New("constraint record")
	}
	c := Constraint{Name: name, Kind: ConstraintKind(raw[1]), Entity: Entity(raw[2])}
	rest := raw[3:]
	var err error
	c.Label, rest, err = takeString(rest)
	if err != nil {
		return Constraint{}, err
	}
	c.Prop, rest, err = takeString(rest)
	if err != nil {
		return Constraint{}, err
	}
	c.TypeName, rest, err = takeString(rest)
	if err != nil {
		return Constraint{}, err
	}
	c.IndexName, rest, err = takeString(rest)
	if err != nil {
		return Constraint{}, err
	}
	return c, nil
}

func encodeStats(st Stats) []byte {
	buf := []byte{1}
	buf = appendU64(buf, uint64(st.Nodes))
	buf = appendU64(buf, uint64(st.Edges))
	buf = appendI64Map(buf, st.Labels)
	buf = appendI64Map(buf, st.EdgeTypes)
	names := make([]string, 0, len(st.Props))
	for k := range st.Props {
		names = append(names, k)
	}
	sort.Strings(names)
	buf = binary.AppendUvarint(buf, uint64(len(names)))
	for _, k := range names {
		buf = appendString(buf, k)
		buf = appendHist(buf, st.Props[k])
	}
	return buf
}

func decodeStats(raw []byte) (Stats, error) {
	st := Stats{Labels: map[string]int64{}, EdgeTypes: map[string]int64{}, Props: map[string]Histogram{}}
	if len(raw) == 0 {
		return st, nil
	}
	if raw[0] != 1 {
		return Stats{}, errors.New("stats record")
	}
	rest := raw[1:]
	n, rest, err := takeU64(rest)
	if err != nil {
		return Stats{}, err
	}
	st.Nodes = int64(n)
	n, rest, err = takeU64(rest)
	if err != nil {
		return Stats{}, err
	}
	st.Edges = int64(n)
	st.Labels, rest, err = takeI64Map(rest)
	if err != nil {
		return Stats{}, err
	}
	st.EdgeTypes, rest, err = takeI64Map(rest)
	if err != nil {
		return Stats{}, err
	}
	count, r := binary.Uvarint(rest)
	if r <= 0 {
		return Stats{}, errors.New("stats record")
	}
	rest = rest[r:]
	for i := uint64(0); i < count; i++ {
		k, next, err := takeString(rest)
		if err != nil {
			return Stats{}, err
		}
		h, next, err := takeHist(next)
		if err != nil {
			return Stats{}, err
		}
		st.Props[k] = h
		rest = next
	}
	return st, nil
}

func appendHist(buf []byte, h Histogram) []byte {
	buf = appendI64(buf, h.Count)
	buf = appendI64(buf, h.NDV)
	buf = appendI64(buf, h.Nulls)
	buf = appendBytes(buf, h.Min)
	buf = appendBytes(buf, h.Max)
	buf = binary.AppendUvarint(buf, uint64(len(h.Buckets)))
	for _, b := range h.Buckets {
		buf = appendBytes(buf, b.Upper)
		buf = appendI64(buf, b.Count)
		buf = appendI64(buf, b.NDV)
	}
	return buf
}

func takeHist(b []byte) (Histogram, []byte, error) {
	var h Histogram
	var err error
	h.Count, b, err = takeI64(b)
	if err != nil {
		return Histogram{}, nil, err
	}
	h.NDV, b, err = takeI64(b)
	if err != nil {
		return Histogram{}, nil, err
	}
	h.Nulls, b, err = takeI64(b)
	if err != nil {
		return Histogram{}, nil, err
	}
	h.Min, b, err = takeBytes(b)
	if err != nil {
		return Histogram{}, nil, err
	}
	h.Max, b, err = takeBytes(b)
	if err != nil {
		return Histogram{}, nil, err
	}
	n, r := binary.Uvarint(b)
	if r <= 0 {
		return Histogram{}, nil, errors.New("histogram")
	}
	b = b[r:]
	for i := uint64(0); i < n; i++ {
		var bk Bucket
		bk.Upper, b, err = takeBytes(b)
		if err != nil {
			return Histogram{}, nil, err
		}
		bk.Count, b, err = takeI64(b)
		if err != nil {
			return Histogram{}, nil, err
		}
		bk.NDV, b, err = takeI64(b)
		if err != nil {
			return Histogram{}, nil, err
		}
		h.Buckets = append(h.Buckets, bk)
	}
	return h, b, nil
}

func appendI64Map(buf []byte, m map[string]int64) []byte {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	buf = binary.AppendUvarint(buf, uint64(len(names)))
	for _, k := range names {
		buf = appendString(buf, k)
		buf = appendI64(buf, m[k])
	}
	return buf
}

func takeI64Map(b []byte) (map[string]int64, []byte, error) {
	n, r := binary.Uvarint(b)
	if r <= 0 {
		return nil, nil, errors.New("stats map")
	}
	b = b[r:]
	out := map[string]int64{}
	for i := uint64(0); i < n; i++ {
		k, next, err := takeString(b)
		if err != nil {
			return nil, nil, err
		}
		v, next, err := takeI64(next)
		if err != nil {
			return nil, nil, err
		}
		out[k] = v
		b = next
	}
	return out, b, nil
}

func appendString(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func takeString(b []byte) (string, []byte, error) {
	n, r := binary.Uvarint(b)
	if r <= 0 || uint64(len(b)-r) < n {
		return "", nil, errors.New("catalog string")
	}
	return string(b[r : r+int(n)]), b[r+int(n):], nil
}

func appendBytes(buf []byte, s []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func takeBytes(b []byte) ([]byte, []byte, error) {
	n, r := binary.Uvarint(b)
	if r <= 0 || uint64(len(b)-r) < n {
		return nil, nil, errors.New("catalog bytes")
	}
	out := append([]byte(nil), b[r:r+int(n)]...)
	return out, b[r+int(n):], nil
}

func appendU64(buf []byte, n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return append(buf, b[:]...)
}

func takeU64(b []byte) (uint64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, errors.New("catalog u64")
	}
	return binary.BigEndian.Uint64(b[:8]), b[8:], nil
}

func appendI64(buf []byte, n int64) []byte {
	return appendU64(buf, uint64(n))
}

func takeI64(b []byte) (int64, []byte, error) {
	n, rest, err := takeU64(b)
	return int64(n), rest, err
}

func sortIndexes(xs []Index) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].Name < xs[j].Name })
}

func sortConstraints(xs []Constraint) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].Name < xs[j].Name })
}

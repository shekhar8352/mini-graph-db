package graphstore

import (
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

const nodeRecVersion byte = 1

func encodeNode(labelIDs []uint32, props []Prop) []byte {
	w := []byte{nodeRecVersion}
	w = binary.AppendUvarint(w, uint64(len(labelIDs)))
	for _, id := range labelIDs {
		w = binary.AppendUvarint(w, uint64(id))
	}
	return appendProps(w, props)
}

func encodeEdge(from, to uint64, typeID uint32, props []Prop) []byte {
	w := []byte{nodeRecVersion}
	w = binary.AppendUvarint(w, from)
	w = binary.AppendUvarint(w, to)
	w = binary.AppendUvarint(w, uint64(typeID))
	return appendProps(w, props)
}

func appendProps(w []byte, props []Prop) []byte {
	w = binary.AppendUvarint(w, uint64(len(props)))
	for _, p := range props {
		w = binary.AppendUvarint(w, uint64(p.ID))
		rec := value.EncodeRecord(p.Value)
		w = binary.AppendUvarint(w, uint64(len(rec)))
		w = append(w, rec...)
	}
	return w
}

type decodedNode struct {
	LabelIDs []uint32
	Props    []Prop
}

type decodedEdge struct {
	From   uint64
	To     uint64
	TypeID uint32
	Props  []Prop
}

func decodeNode(b []byte) (decodedNode, error) {
	r := &rbuf{b: b}
	ver, err := r.byte()
	if err != nil {
		return decodedNode{}, err
	}
	if ver != nodeRecVersion {
		return decodedNode{}, corrupt("node record version")
	}
	n, err := r.uvar()
	if err != nil {
		return decodedNode{}, err
	}
	labels := make([]uint32, 0, n)
	for i := uint64(0); i < n; i++ {
		id, err := r.uvar()
		if err != nil {
			return decodedNode{}, err
		}
		if id > uint64(^uint32(0)) {
			return decodedNode{}, corrupt("label id")
		}
		labels = append(labels, uint32(id))
	}
	props, err := r.props()
	if err != nil {
		return decodedNode{}, err
	}
	if r.left() != 0 {
		return decodedNode{}, corrupt("node record")
	}
	return decodedNode{LabelIDs: labels, Props: props}, nil
}

func decodeEdge(b []byte) (decodedEdge, error) {
	r := &rbuf{b: b}
	ver, err := r.byte()
	if err != nil {
		return decodedEdge{}, err
	}
	if ver != nodeRecVersion {
		return decodedEdge{}, corrupt("edge record version")
	}
	from, err := r.uvar()
	if err != nil {
		return decodedEdge{}, err
	}
	to, err := r.uvar()
	if err != nil {
		return decodedEdge{}, err
	}
	typeID, err := r.uvar()
	if err != nil {
		return decodedEdge{}, err
	}
	if typeID > uint64(^uint32(0)) {
		return decodedEdge{}, corrupt("edge type id")
	}
	props, err := r.props()
	if err != nil {
		return decodedEdge{}, err
	}
	if r.left() != 0 {
		return decodedEdge{}, corrupt("edge record")
	}
	return decodedEdge{From: from, To: to, TypeID: uint32(typeID), Props: props}, nil
}

type rbuf struct {
	b []byte
	i int
}

func (r *rbuf) byte() (byte, error) {
	if r.i >= len(r.b) {
		return 0, corrupt("truncated record")
	}
	c := r.b[r.i]
	r.i++
	return c, nil
}

func (r *rbuf) uvar() (uint64, error) {
	v, n := binary.Uvarint(r.b[r.i:])
	if n <= 0 {
		return 0, corrupt("truncated varint")
	}
	r.i += n
	return v, nil
}

func (r *rbuf) props() ([]Prop, error) {
	n, err := r.uvar()
	if err != nil {
		return nil, err
	}
	out := make([]Prop, 0, n)
	for i := uint64(0); i < n; i++ {
		id, err := r.uvar()
		if err != nil {
			return nil, err
		}
		if id > uint64(^uint32(0)) {
			return nil, corrupt("property id")
		}
		ln, err := r.uvar()
		if err != nil {
			return nil, err
		}
		if uint64(r.left()) < ln {
			return nil, corrupt("property value")
		}
		raw := append([]byte(nil), r.b[r.i:r.i+int(ln)]...)
		r.i += int(ln)
		val, err := value.DecodeRecord(raw)
		if err != nil {
			return nil, corrupt("property value")
		}
		out = append(out, Prop{ID: uint32(id), Value: val})
	}
	return out, nil
}

func (r *rbuf) left() int {
	return len(r.b) - r.i
}

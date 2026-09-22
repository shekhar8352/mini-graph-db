package value

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RecordFormatVersion is the leading byte of EncodeRecord.
// Version 1 is the Phase 1 layout. Newer versions are rejected.
const RecordFormatVersion byte = 1

// EncodeRecord returns a compact, kind-preserving encoding of v.
// Nested values do not repeat the version byte. The encoding is not
// memcomparable; use EncodeKey for index order.
func EncodeRecord(v Value) []byte {
	return appendRecord([]byte{RecordFormatVersion}, v)
}

// DecodeRecord reverses EncodeRecord. The buffer must be exactly one record.
func DecodeRecord(b []byte) (Value, error) {
	if len(b) == 0 {
		return Value{}, fmt.Errorf("value: truncated record")
	}
	if b[0] != RecordFormatVersion {
		return Value{}, fmt.Errorf("value: unsupported record format version %d", b[0])
	}
	v, rest, err := decodeRecord(b[1:])
	if err != nil {
		return Value{}, err
	}
	if len(rest) != 0 {
		return Value{}, fmt.Errorf("value: trailing bytes in record")
	}
	return v, nil
}

func appendRecord(dst []byte, v Value) []byte {
	dst = append(dst, byte(v.kind))
	switch v.kind {
	case KindNull:
		return dst
	case KindBool:
		if v.b {
			return append(dst, 1)
		}
		return append(dst, 0)
	case KindInt:
		return binary.AppendUvarint(dst, zigzag(v.i))
	case KindFloat:
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v.f))
		return append(dst, buf[:]...)
	case KindString:
		dst = binary.AppendUvarint(dst, uint64(len(v.s)))
		return append(dst, v.s...)
	case KindBytes:
		dst = binary.AppendUvarint(dst, uint64(len(v.raw)))
		return append(dst, v.raw...)
	case KindDate:
		return binary.AppendUvarint(dst, zigzag(v.i))
	case KindDateTime:
		dst = binary.AppendUvarint(dst, zigzag(v.i))
		if !v.zoneSet {
			return append(dst, 0)
		}
		dst = append(dst, 1)
		return binary.AppendUvarint(dst, zigzag(int64(v.zoneOff)))
	case KindDuration:
		dst = binary.AppendUvarint(dst, zigzag(v.i))
		dst = binary.AppendUvarint(dst, zigzag(v.day))
		return binary.AppendUvarint(dst, zigzag(v.nsec))
	case KindList:
		dst = binary.AppendUvarint(dst, uint64(len(v.list)))
		for _, el := range v.list {
			dst = appendRecord(dst, el)
		}
		return dst
	case KindMap:
		dst = binary.AppendUvarint(dst, uint64(len(v.kvs)))
		for _, e := range v.kvs {
			dst = binary.AppendUvarint(dst, uint64(len(e.key)))
			dst = append(dst, e.key...)
			dst = appendRecord(dst, e.val)
		}
		return dst
	case KindNode, KindEdge:
		return binary.AppendUvarint(dst, v.u)
	case KindPath:
		dst = binary.AppendUvarint(dst, uint64(len(v.nodes)))
		for _, id := range v.nodes {
			dst = binary.AppendUvarint(dst, id)
		}
		dst = binary.AppendUvarint(dst, uint64(len(v.edges)))
		for _, id := range v.edges {
			dst = binary.AppendUvarint(dst, id)
		}
		return dst
	default:
		return dst
	}
}

func decodeRecord(b []byte) (Value, []byte, error) {
	if len(b) == 0 {
		return Value{}, nil, fmt.Errorf("value: truncated record")
	}
	k := Kind(b[0])
	b = b[1:]
	switch k {
	case KindNull:
		return Null(), b, nil
	case KindBool:
		if len(b) < 1 {
			return Value{}, nil, fmt.Errorf("value: truncated record")
		}
		return Bool(b[0] != 0), b[1:], nil
	case KindInt:
		n, rest, err := readZigzagInt(b)
		if err != nil {
			return Value{}, nil, err
		}
		return Int(n), rest, nil
	case KindFloat:
		if len(b) < 8 {
			return Value{}, nil, fmt.Errorf("value: truncated record")
		}
		return Float(math.Float64frombits(binary.LittleEndian.Uint64(b[:8]))), b[8:], nil
	case KindString:
		s, rest, err := readBlob(b)
		if err != nil {
			return Value{}, nil, err
		}
		return String(string(s)), rest, nil
	case KindBytes:
		s, rest, err := readBlob(b)
		if err != nil {
			return Value{}, nil, err
		}
		return Bytes(s), rest, nil
	case KindDate:
		n, rest, err := readZigzagInt(b)
		if err != nil {
			return Value{}, nil, err
		}
		return Value{kind: KindDate, i: n}, rest, nil
	case KindDateTime:
		n, rest, err := readZigzagInt(b)
		if err != nil {
			return Value{}, nil, err
		}
		if len(rest) < 1 {
			return Value{}, nil, fmt.Errorf("value: truncated record")
		}
		flag := rest[0]
		rest = rest[1:]
		if flag == 0 {
			return Value{kind: KindDateTime, i: n}, rest, nil
		}
		off, rest, err := readZigzagInt(rest)
		if err != nil {
			return Value{}, nil, err
		}
		if off < math.MinInt32 || off > math.MaxInt32 {
			return Value{}, nil, fmt.Errorf("value: datetime offset out of range")
		}
		return Value{kind: KindDateTime, i: n, zoneSet: true, zoneOff: int32(off)}, rest, nil
	case KindDuration:
		months, rest, err := readZigzagInt(b)
		if err != nil {
			return Value{}, nil, err
		}
		days, rest, err := readZigzagInt(rest)
		if err != nil {
			return Value{}, nil, err
		}
		nanos, rest, err := readZigzagInt(rest)
		if err != nil {
			return Value{}, nil, err
		}
		return Duration(months, days, nanos), rest, nil
	case KindList:
		n, rest, err := readUvarint(b)
		if err != nil {
			return Value{}, nil, err
		}
		elems := make([]Value, 0, n)
		for i := uint64(0); i < n; i++ {
			el, r, err := decodeRecord(rest)
			if err != nil {
				return Value{}, nil, err
			}
			elems = append(elems, el)
			rest = r
		}
		return List(elems...), rest, nil
	case KindMap:
		n, rest, err := readUvarint(b)
		if err != nil {
			return Value{}, nil, err
		}
		m := make(map[string]Value, n)
		for i := uint64(0); i < n; i++ {
			kb, r, err := readBlob(rest)
			if err != nil {
				return Value{}, nil, err
			}
			val, r, err := decodeRecord(r)
			if err != nil {
				return Value{}, nil, err
			}
			m[string(kb)] = val
			rest = r
		}
		return Map(m), rest, nil
	case KindNode:
		id, rest, err := readUvarint(b)
		if err != nil {
			return Value{}, nil, err
		}
		return NodeRef(id), rest, nil
	case KindEdge:
		id, rest, err := readUvarint(b)
		if err != nil {
			return Value{}, nil, err
		}
		return EdgeRef(id), rest, nil
	case KindPath:
		nn, rest, err := readUvarint(b)
		if err != nil {
			return Value{}, nil, err
		}
		nodes := make([]uint64, 0, nn)
		for i := uint64(0); i < nn; i++ {
			id, r, err := readUvarint(rest)
			if err != nil {
				return Value{}, nil, err
			}
			nodes = append(nodes, id)
			rest = r
		}
		ne, rest, err := readUvarint(rest)
		if err != nil {
			return Value{}, nil, err
		}
		edges := make([]uint64, 0, ne)
		for i := uint64(0); i < ne; i++ {
			id, r, err := readUvarint(rest)
			if err != nil {
				return Value{}, nil, err
			}
			edges = append(edges, id)
			rest = r
		}
		p, err := PathOf(nodes, edges)
		if err != nil {
			return Value{}, nil, err
		}
		return p, rest, nil
	default:
		return Value{}, nil, fmt.Errorf("value: unknown record kind %d", k)
	}
}

func zigzag(n int64) uint64 {
	return uint64((n << 1) ^ (n >> 63))
}

func unzigzag(n uint64) int64 {
	return int64(n>>1) ^ -int64(n&1)
}

func readUvarint(b []byte) (uint64, []byte, error) {
	n, r := binary.Uvarint(b)
	if r <= 0 {
		return 0, nil, fmt.Errorf("value: truncated record")
	}
	return n, b[r:], nil
}

func readZigzagInt(b []byte) (int64, []byte, error) {
	n, rest, err := readUvarint(b)
	if err != nil {
		return 0, nil, err
	}
	return unzigzag(n), rest, nil
}

func readBlob(b []byte) ([]byte, []byte, error) {
	n, rest, err := readUvarint(b)
	if err != nil {
		return nil, nil, err
	}
	if uint64(len(rest)) < n {
		return nil, nil, fmt.Errorf("value: truncated record")
	}
	out := append([]byte(nil), rest[:n]...)
	return out, rest[n:], nil
}

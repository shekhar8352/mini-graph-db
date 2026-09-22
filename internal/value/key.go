package value

import (
	"encoding/binary"
	"fmt"
	"math"
)

// KeyFormatVersion is the memcomparable key encoding version.
// Key bytes have no version prefix, because a prefix would sort before every
// value. Bumping this means rebuilding indexes.
const KeyFormatVersion = 1

// Key tags are ordered to match Compare. 0x00 is reserved as a terminator
// for strings, lists, maps, and paths, so no tag uses it.
const (
	keyNull     byte = 0x01
	keyBool     byte = 0x02
	keyNumber   byte = 0x03
	keyString   byte = 0x04
	keyBytes    byte = 0x05
	keyDate     byte = 0x06
	keyDateTime byte = 0x07
	keyDuration byte = 0x08
	keyList     byte = 0x09
	keyMap      byte = 0x0A
	keyNode     byte = 0x0B
	keyEdge     byte = 0x0C
	keyPath     byte = 0x0D
)

// EncodeKey returns a memcomparable encoding of v.
// bytes.Compare(EncodeKey(a), EncodeKey(b)) agrees with Compare(a, b).
//
// Integers use a sign-bit flip into the float64 total order (the IEEE
// sign-bit trick), with a big-endian gap offset when the integer is not an
// exact float64. Floats use that same IEEE ordering, so int 1 and float 1.0
// encode identically. Strings and byte strings escape 0x00 as 0x00 0xFF and
// end with a 0x00 terminator. Composite values are self-delimiting, so
// EncodeComposite can concatenate them.
func EncodeKey(v Value) []byte {
	return appendKey(nil, v)
}

// DecodeKey reverses EncodeKey. The input must be exactly one value.
//
// Decoding is canonical for numbers: an exact int64 float comes back as Int,
// −0 comes back as Int(0), and every NaN comes back as a single NaN payload.
// Equal(v, DecodeKey(EncodeKey(v))) holds for every value. Kind is preserved
// except for that numeric canonicalization.
func DecodeKey(b []byte) (Value, error) {
	v, rest, err := decodeKey(b)
	if err != nil {
		return Value{}, err
	}
	if len(rest) != 0 {
		return Value{}, fmt.Errorf("value: trailing bytes in key")
	}
	return v, nil
}

// EncodeComposite concatenates key encodings. The result orders
// lexicographically by the value sequence because each encoding is
// self-delimiting and no encoding is a prefix of another.
func EncodeComposite(vs ...Value) []byte {
	var buf []byte
	for _, v := range vs {
		buf = appendKey(buf, v)
	}
	return buf
}

// DecodeComposite reads values until b is exhausted.
func DecodeComposite(b []byte) ([]Value, error) {
	var out []Value
	for len(b) > 0 {
		v, rest, err := decodeKey(b)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		b = rest
	}
	return out, nil
}

func appendKey(dst []byte, v Value) []byte {
	switch v.kind {
	case KindNull:
		return append(dst, keyNull)
	case KindBool:
		bit := byte(0)
		if v.b {
			bit = 1
		}
		return append(dst, keyBool, bit)
	case KindInt:
		lower, off := splitIntForKey(v.i)
		return appendNumber(dst, lower, off)
	case KindFloat:
		return appendNumber(dst, canonicalFloat(v.f), 0)
	case KindString:
		dst = append(dst, keyString)
		return appendEscaped(dst, v.s)
	case KindBytes:
		dst = append(dst, keyBytes)
		return appendEscapedBytes(dst, v.raw)
	case KindDate:
		dst = append(dst, keyDate)
		return appendSignFlip32(dst, int32(v.i))
	case KindDateTime:
		dst = append(dst, keyDateTime)
		dst = appendSignFlip64(dst, v.i)
		if !v.zoneSet {
			return append(dst, 0)
		}
		dst = append(dst, 1)
		return appendSignFlip32(dst, v.zoneOff)
	case KindDuration:
		dst = append(dst, keyDuration)
		dst = appendSignFlip64(dst, v.i)
		dst = appendSignFlip64(dst, v.day)
		return appendSignFlip64(dst, v.nsec)
	case KindList:
		dst = append(dst, keyList)
		for _, el := range v.list {
			dst = appendKey(dst, el)
		}
		return append(dst, 0x00)
	case KindMap:
		dst = append(dst, keyMap)
		for _, e := range v.kvs {
			dst = appendKey(dst, String(e.key))
			dst = appendKey(dst, e.val)
		}
		return append(dst, 0x00)
	case KindNode:
		dst = append(dst, keyNode)
		return appendU64(dst, v.u)
	case KindEdge:
		dst = append(dst, keyEdge)
		return appendU64(dst, v.u)
	case KindPath:
		dst = append(dst, keyPath)
		for i, id := range v.nodes {
			dst = append(dst, 0x01)
			dst = appendU64(dst, id)
			if i < len(v.edges) {
				dst = append(dst, 0x02)
				dst = appendU64(dst, v.edges[i])
			}
		}
		return append(dst, 0x00)
	default:
		return append(dst, 0xFF)
	}
}

func appendNumber(dst []byte, f float64, off uint16) []byte {
	dst = append(dst, keyNumber)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], orderedFloat(f))
	dst = append(dst, buf[:]...)
	var ob [2]byte
	binary.BigEndian.PutUint16(ob[:], off)
	return append(dst, ob[:]...)
}

func appendEscaped(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x00 {
			dst = append(dst, 0x00, 0xFF)
			continue
		}
		dst = append(dst, s[i])
	}
	return append(dst, 0x00)
}

func appendEscapedBytes(dst []byte, b []byte) []byte {
	for _, c := range b {
		if c == 0x00 {
			dst = append(dst, 0x00, 0xFF)
			continue
		}
		dst = append(dst, c)
	}
	return append(dst, 0x00)
}

func appendSignFlip64(dst []byte, n int64) []byte {
	u := uint64(n) ^ (1 << 63)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], u)
	return append(dst, buf[:]...)
}

func appendSignFlip32(dst []byte, n int32) []byte {
	u := uint32(n) ^ (1 << 31)
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], u)
	return append(dst, buf[:]...)
}

func appendU64(dst []byte, n uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	return append(dst, buf[:]...)
}

func decodeKey(b []byte) (Value, []byte, error) {
	if len(b) == 0 {
		return Value{}, nil, fmt.Errorf("value: truncated key")
	}
	tag := b[0]
	b = b[1:]
	switch tag {
	case keyNull:
		return Null(), b, nil
	case keyBool:
		if len(b) < 1 {
			return Value{}, nil, fmt.Errorf("value: truncated key")
		}
		switch b[0] {
		case 0:
			return Bool(false), b[1:], nil
		case 1:
			return Bool(true), b[1:], nil
		default:
			return Value{}, nil, fmt.Errorf("value: invalid bool key")
		}
	case keyNumber:
		return decodeNumber(b)
	case keyString:
		s, rest, err := decodeEscaped(b)
		if err != nil {
			return Value{}, nil, err
		}
		return String(s), rest, nil
	case keyBytes:
		s, rest, err := decodeEscaped(b)
		if err != nil {
			return Value{}, nil, err
		}
		return Bytes([]byte(s)), rest, nil
	case keyDate:
		n, rest, err := decodeSignFlip32(b)
		if err != nil {
			return Value{}, nil, err
		}
		return Value{kind: KindDate, i: int64(n)}, rest, nil
	case keyDateTime:
		return decodeDateTime(b)
	case keyDuration:
		return decodeDuration(b)
	case keyList:
		return decodeList(b)
	case keyMap:
		return decodeMap(b)
	case keyNode:
		id, rest, err := decodeU64(b)
		if err != nil {
			return Value{}, nil, err
		}
		return NodeRef(id), rest, nil
	case keyEdge:
		id, rest, err := decodeU64(b)
		if err != nil {
			return Value{}, nil, err
		}
		return EdgeRef(id), rest, nil
	case keyPath:
		return decodePath(b)
	default:
		return Value{}, nil, fmt.Errorf("value: unknown key tag %d", tag)
	}
}

func decodeNumber(b []byte) (Value, []byte, error) {
	if len(b) < 10 {
		return Value{}, nil, fmt.Errorf("value: truncated key")
	}
	bits := binary.BigEndian.Uint64(b[:8])
	off := binary.BigEndian.Uint16(b[8:10])
	b = b[10:]
	f := unorderedFloat(bits)
	if off == 0 {
		if math.IsNaN(f) {
			return Float(math.NaN()), b, nil
		}
		if i, ok := floatIsExactInt64(f); ok {
			return Int(i), b, nil
		}
		return Float(f), b, nil
	}
	li, ok := floatIsExactInt64(f)
	if !ok {
		return Value{}, nil, fmt.Errorf("value: invalid number key")
	}
	return Int(li + int64(off)), b, nil
}

func decodeEscaped(b []byte) (string, []byte, error) {
	out := make([]byte, 0, len(b))
	for len(b) > 0 {
		c := b[0]
		b = b[1:]
		if c != 0x00 {
			out = append(out, c)
			continue
		}
		if len(b) > 0 && b[0] == 0xFF {
			out = append(out, 0x00)
			b = b[1:]
			continue
		}
		return string(out), b, nil
	}
	return "", nil, fmt.Errorf("value: truncated key")
}

func decodeSignFlip64(b []byte) (int64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, fmt.Errorf("value: truncated key")
	}
	u := binary.BigEndian.Uint64(b[:8]) ^ (1 << 63)
	return int64(u), b[8:], nil
}

func decodeSignFlip32(b []byte) (int32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, fmt.Errorf("value: truncated key")
	}
	u := binary.BigEndian.Uint32(b[:4]) ^ (1 << 31)
	return int32(u), b[4:], nil
}

func decodeU64(b []byte) (uint64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, fmt.Errorf("value: truncated key")
	}
	return binary.BigEndian.Uint64(b[:8]), b[8:], nil
}

func decodeDateTime(b []byte) (Value, []byte, error) {
	n, rest, err := decodeSignFlip64(b)
	if err != nil {
		return Value{}, nil, err
	}
	if len(rest) < 1 {
		return Value{}, nil, fmt.Errorf("value: truncated key")
	}
	flag := rest[0]
	rest = rest[1:]
	switch flag {
	case 0:
		return Value{kind: KindDateTime, i: n}, rest, nil
	case 1:
		off, rest, err := decodeSignFlip32(rest)
		if err != nil {
			return Value{}, nil, err
		}
		return Value{kind: KindDateTime, i: n, zoneSet: true, zoneOff: off}, rest, nil
	default:
		return Value{}, nil, fmt.Errorf("value: invalid datetime key")
	}
}

func decodeDuration(b []byte) (Value, []byte, error) {
	months, b, err := decodeSignFlip64(b)
	if err != nil {
		return Value{}, nil, err
	}
	days, b, err := decodeSignFlip64(b)
	if err != nil {
		return Value{}, nil, err
	}
	nanos, b, err := decodeSignFlip64(b)
	if err != nil {
		return Value{}, nil, err
	}
	return Duration(months, days, nanos), b, nil
}

func decodeList(b []byte) (Value, []byte, error) {
	var elems []Value
	for {
		if len(b) == 0 {
			return Value{}, nil, fmt.Errorf("value: truncated key")
		}
		if b[0] == 0x00 {
			return List(elems...), b[1:], nil
		}
		el, rest, err := decodeKey(b)
		if err != nil {
			return Value{}, nil, err
		}
		elems = append(elems, el)
		b = rest
	}
}

func decodeMap(b []byte) (Value, []byte, error) {
	m := map[string]Value{}
	var prev string
	first := true
	for {
		if len(b) == 0 {
			return Value{}, nil, fmt.Errorf("value: truncated key")
		}
		if b[0] == 0x00 {
			return Map(m), b[1:], nil
		}
		key, rest, err := decodeKey(b)
		if err != nil {
			return Value{}, nil, err
		}
		if key.kind != KindString {
			return Value{}, nil, fmt.Errorf("value: map key is not a string")
		}
		val, rest, err := decodeKey(rest)
		if err != nil {
			return Value{}, nil, err
		}
		if !first && key.s < prev {
			return Value{}, nil, fmt.Errorf("value: map keys out of order")
		}
		prev = key.s
		first = false
		m[key.s] = val
		b = rest
	}
}

func decodePath(b []byte) (Value, []byte, error) {
	var nodes, edges []uint64
	expectNode := true
	for {
		if len(b) == 0 {
			return Value{}, nil, fmt.Errorf("value: truncated key")
		}
		switch b[0] {
		case 0x00:
			if len(nodes) > 0 && expectNode {
				return Value{}, nil, fmt.Errorf("value: path ended on an edge")
			}
			p, err := PathOf(nodes, edges)
			if err != nil {
				return Value{}, nil, err
			}
			return p, b[1:], nil
		case 0x01:
			if !expectNode {
				return Value{}, nil, fmt.Errorf("value: path expected an edge")
			}
			id, rest, err := decodeU64(b[1:])
			if err != nil {
				return Value{}, nil, err
			}
			nodes = append(nodes, id)
			b = rest
			expectNode = false
		case 0x02:
			if expectNode {
				return Value{}, nil, fmt.Errorf("value: path expected a node")
			}
			id, rest, err := decodeU64(b[1:])
			if err != nil {
				return Value{}, nil, err
			}
			edges = append(edges, id)
			b = rest
			expectNode = true
		default:
			return Value{}, nil, fmt.Errorf("value: invalid path key")
		}
	}
}

func sortKVs(kvs []kv) {
	// Insertion sort: property maps are small, and this stays in stdlib without extra imports.
	for i := 1; i < len(kvs); i++ {
		j := i
		for j > 0 && kvs[j].key < kvs[j-1].key {
			kvs[j], kvs[j-1] = kvs[j-1], kvs[j]
			j--
		}
	}
}

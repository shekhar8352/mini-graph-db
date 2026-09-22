package value

import "math"

// Hash returns a 64-bit FNV-1a hash consistent with Equal:
// Equal(a, b) implies Hash(a) == Hash(b).
//
// The hash is stable across processes. It is not a cryptographic hash.
// Numerically equal ints and floats share a hash. Every NaN shares one hash.
// −0 shares the hash of 0.
func Hash(v Value) uint64 {
	if v.kind == KindInt || v.kind == KindFloat {
		return hashNumber(v)
	}
	h := hashByte(fnvOffset, byte(v.kind))
	switch v.kind {
	case KindNull:
		return h
	case KindBool:
		if v.b {
			return hashByte(h, 1)
		}
		return hashByte(h, 0)
	case KindString:
		return hashString(h, v.s)
	case KindBytes:
		return hashBytes(h, v.raw)
	case KindDate:
		return hashUint64(h, uint64(v.i))
	case KindDateTime:
		h = hashUint64(h, uint64(v.i))
		if !v.zoneSet {
			return hashByte(h, 0)
		}
		h = hashByte(h, 1)
		return hashUint64(h, uint64(uint32(v.zoneOff)))
	case KindDuration:
		h = hashUint64(h, uint64(v.i))
		h = hashUint64(h, uint64(v.day))
		return hashUint64(h, uint64(v.nsec))
	case KindList:
		h = hashUint64(h, uint64(len(v.list)))
		for _, el := range v.list {
			h = hashUint64(h, Hash(el))
		}
		return h
	case KindMap:
		h = hashUint64(h, uint64(len(v.kvs)))
		for _, e := range v.kvs {
			h = hashString(h, e.key)
			h = hashUint64(h, Hash(e.val))
		}
		return h
	case KindNode, KindEdge:
		return hashUint64(h, v.u)
	case KindPath:
		h = hashUint64(h, uint64(len(v.nodes)))
		for _, id := range v.nodes {
			h = hashUint64(h, id)
		}
		h = hashUint64(h, uint64(len(v.edges)))
		for _, id := range v.edges {
			h = hashUint64(h, id)
		}
		return h
	default:
		return h
	}
}

const (
	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
)

func hashNumber(v Value) uint64 {
	// Shared domain tag so int 1 and float 1.0 collide, as Equal requires.
	h := hashByte(fnvOffset, byte(KindInt))
	switch v.kind {
	case KindInt:
		return hashUint64(h, uint64(v.i))
	default:
		f := canonicalFloat(v.f)
		if math.IsNaN(f) {
			return hashUint64(h, 0x7ff8000000000001)
		}
		if i, ok := floatIsExactInt64(f); ok {
			return hashUint64(h, uint64(i))
		}
		return hashUint64(h, math.Float64bits(f))
	}
}

func hashByte(h uint64, b byte) uint64 {
	h ^= uint64(b)
	return h * fnvPrime
}

func hashBytes(h uint64, b []byte) uint64 {
	for _, c := range b {
		h = hashByte(h, c)
	}
	return h
}

func hashString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = hashByte(h, s[i])
	}
	return h
}

func hashUint64(h, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h = hashByte(h, byte(v))
		v >>= 8
	}
	return h
}

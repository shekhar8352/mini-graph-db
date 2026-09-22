package value

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestKeyOrderProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	special := allSamples(t)
	for n := 0; n < 100_000; n++ {
		a := pickValue(rng, special)
		b := pickValue(rng, special)
		c := Compare(a, b)
		bc := bytes.Compare(EncodeKey(a), EncodeKey(b))
		if sign(c) != sign(bc) {
			t.Fatalf("pair %d: compare(%s, %s)=%d bytes=%d", n, a, b, c, bc)
		}
		if c == 0 && Hash(a) != Hash(b) {
			t.Fatalf("pair %d: equal values hashed differently: %s %s", n, a, b)
		}
	}
}

func pickValue(rng *rand.Rand, special []Value) Value {
	if rng.Intn(3) == 0 {
		return special[rng.Intn(len(special))]
	}
	return randomValue(rng, 2)
}

func randomValue(rng *rand.Rand, depth int) Value {
	kinds := []Kind{
		KindNull, KindBool, KindInt, KindFloat, KindString, KindBytes,
		KindDate, KindDateTime, KindDuration, KindList, KindMap, KindNode, KindEdge, KindPath,
	}
	if depth == 0 {
		kinds = kinds[:9]
	}
	switch kinds[rng.Intn(len(kinds))] {
	case KindNull:
		return Null()
	case KindBool:
		return Bool(rng.Intn(2) == 0)
	case KindInt:
		return randomInt(rng)
	case KindFloat:
		return randomFloat(rng)
	case KindString:
		return String(randomText(rng))
	case KindBytes:
		n := rng.Intn(8)
		b := make([]byte, n)
		if _, err := rng.Read(b); err != nil {
			return Bytes(nil)
		}
		return Bytes(b)
	case KindDate:
		y := rng.Intn(4000) - 1000
		m := time.Month(rng.Intn(12) + 1)
		d := rng.Intn(28) + 1
		v, err := Date(y, m, d)
		if err != nil {
			return Null()
		}
		return v
	case KindDateTime:
		sec := rng.Int63n(200*365*24*3600) - 100*365*24*3600
		v, err := DateTimeUTC(time.Unix(sec, rng.Int63n(1e9)))
		if err != nil {
			return Null()
		}
		if rng.Intn(2) == 0 {
			off := (rng.Intn(37) - 18) * 3600
			v, err = DateTimeOffset(time.Unix(sec, 0), off)
			if err != nil {
				return Null()
			}
		}
		return v
	case KindDuration:
		return Duration(int64(rng.Intn(40)-20), int64(rng.Intn(80)-40), rng.Int63n(1e15)-5e14)
	case KindList:
		n := rng.Intn(4)
		elems := make([]Value, n)
		for i := range elems {
			elems[i] = randomValue(rng, depth-1)
		}
		return List(elems...)
	case KindMap:
		n := rng.Intn(4)
		m := make(map[string]Value, n)
		for i := 0; i < n; i++ {
			m[randomText(rng)] = randomValue(rng, depth-1)
		}
		return Map(m)
	case KindNode:
		return NodeRef(rng.Uint64())
	case KindEdge:
		return EdgeRef(rng.Uint64())
	default:
		n := rng.Intn(3) + 1
		nodes := make([]uint64, n)
		edges := make([]uint64, n-1)
		for i := range nodes {
			nodes[i] = rng.Uint64()
		}
		for i := range edges {
			edges[i] = rng.Uint64()
		}
		v, err := PathOf(nodes, edges)
		if err != nil {
			return Null()
		}
		return v
	}
}

func randomInt(rng *rand.Rand) Value {
	switch rng.Intn(6) {
	case 0:
		return Int(0)
	case 1:
		return Int(math.MaxInt64)
	case 2:
		return Int(math.MinInt64)
	case 3:
		return Int(1<<53 + int64(rng.Intn(5)))
	case 4:
		return Int(-(1 << 54) - int64(rng.Intn(8)))
	default:
		return Int(rng.Int63() - 1<<62)
	}
}

func randomFloat(rng *rand.Rand) Value {
	switch rng.Intn(6) {
	case 0:
		return Float(math.NaN())
	case 1:
		return Float(math.Inf(1))
	case 2:
		return Float(math.Inf(-1))
	case 3:
		return Float(math.Copysign(0, -1))
	case 4:
		return Float(float64(rng.Intn(10)))
	default:
		return Float(math.Float64frombits(rng.Uint64()))
	}
}

func randomText(rng *rand.Rand) string {
	n := rng.Intn(6)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	return string(b)
}

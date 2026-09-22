package value

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func FuzzRecordRoundTrip(f *testing.F) {
	for _, v := range fuzzSeeds() {
		f.Add(EncodeRecord(v))
	}
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		v := valueFromFuzz(data)
		enc := EncodeRecord(v)
		got, err := DecodeRecord(enc)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Kind() != v.Kind() || !Equal(v, got) {
			t.Fatalf("record %s -> %s", v, got)
		}
		if !bytes.Equal(enc, EncodeRecord(got)) {
			t.Fatal("unstable record")
		}
		_, _ = DecodeRecord(data)
	})
}

func FuzzKeyRoundTrip(f *testing.F) {
	for _, v := range fuzzSeeds() {
		f.Add(EncodeKey(v))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v := valueFromFuzz(data)
		enc := EncodeKey(v)
		got, err := DecodeKey(enc)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !Equal(v, got) {
			t.Fatalf("key %s -> %s", v, got)
		}
		if !bytes.Equal(enc, EncodeKey(got)) {
			t.Fatal("unstable key")
		}
		_, _ = DecodeKey(data)
	})
}

func FuzzKeyOrder(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		mid := len(data) / 2
		a := valueFromFuzz(data[:mid])
		b := valueFromFuzz(data[mid:])
		c := Compare(a, b)
		bc := bytes.Compare(EncodeKey(a), EncodeKey(b))
		if sign(c) != sign(bc) {
			t.Fatalf("compare %d bytes %d for %s vs %s", c, bc, a, b)
		}
	})
}

func fuzzSeeds() []Value {
	d, _ := Date(2025, time.January, 31)
	dt, _ := DateTimeUTC(time.Unix(1_700_000_000, 0))
	p, _ := PathOf([]uint64{1}, nil)
	return []Value{
		Null(), Bool(true), Int(-1), Int(1<<53 + 1),
		Float(math.NaN()), Float(math.Copysign(0, -1)), Float(1.5),
		String("a\x00"), Bytes([]byte{0, 1}),
		d, dt, Duration(1, 2, 3),
		List(Int(1), Null()),
		Map(map[string]Value{"k": String("v")}),
		NodeRef(7), EdgeRef(8), p,
	}
}

func valueFromFuzz(data []byte) Value {
	v, _ := consumeFuzz(data, 0)
	return v
}

func consumeFuzz(data []byte, depth int) (Value, []byte) {
	if len(data) == 0 || depth > 3 {
		return Null(), data
	}
	tag := data[0]
	data = data[1:]
	switch tag % 14 {
	case 0:
		return Null(), data
	case 1:
		return Bool(len(data) > 0 && data[0]%2 == 0), data
	case 2:
		return fuzzInt(data), drop(data, 8)
	case 3:
		return fuzzFloat(data), drop(data, 8)
	case 4:
		n := 0
		if len(data) > 0 {
			n = int(data[0] % 8)
		}
		if n > len(data)-1 {
			n = 0
		}
		s := ""
		if len(data) > 1 {
			s = string(data[1 : 1+n])
		}
		return String(s), drop(data, 1+n)
	case 5:
		n := 0
		if len(data) > 0 {
			n = int(data[0] % 8)
		}
		if len(data) < 1+n {
			n = 0
		}
		var b []byte
		if n > 0 {
			b = data[1 : 1+n]
		}
		return Bytes(b), drop(data, 1+n)
	case 6:
		v, err := Date(2020, time.March, 1)
		if len(data) > 0 {
			day := int(data[0]%28) + 1
			v, err = Date(2000+int(data[0]%50), time.Month(int(data[0]%12)+1), day)
		}
		if err != nil {
			return Null(), data
		}
		return v, drop(data, 1)
	case 7:
		sec := int64(0)
		if len(data) >= 4 {
			sec = int64(int32(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24))
		}
		v, err := DateTimeUTC(time.Unix(sec, 0))
		if err != nil {
			return Null(), data
		}
		return v, drop(data, 4)
	case 8:
		var m, d, n int64
		if len(data) >= 3 {
			m = int64(int8(data[0]))
			d = int64(int8(data[1]))
			n = int64(int8(data[2])) * int64(time.Second)
		}
		return Duration(m, d, n), drop(data, 3)
	case 9:
		n := 0
		if len(data) > 0 {
			n = int(data[0] % 3)
			data = data[1:]
		}
		elems := make([]Value, n)
		for i := 0; i < n; i++ {
			elems[i], data = consumeFuzz(data, depth+1)
		}
		return List(elems...), data
	case 10:
		n := 0
		if len(data) > 0 {
			n = int(data[0] % 3)
			data = data[1:]
		}
		m := make(map[string]Value, n)
		for i := 0; i < n; i++ {
			key := "k"
			if len(data) > 0 {
				key = string(rune('a' + data[0]%26))
				data = data[1:]
			}
			var val Value
			val, data = consumeFuzz(data, depth+1)
			m[key] = val
		}
		return Map(m), data
	case 11:
		return NodeRef(u64fuzz(data)), drop(data, 8)
	case 12:
		return EdgeRef(u64fuzz(data)), drop(data, 8)
	default:
		v, err := PathOf([]uint64{u64fuzz(data)}, nil)
		if err != nil {
			return Null(), data
		}
		return v, drop(data, 8)
	}
}

func fuzzInt(data []byte) Value {
	if len(data) == 0 {
		return Int(0)
	}
	switch data[0] % 5 {
	case 0:
		return Int(math.MaxInt64)
	case 1:
		return Int(math.MinInt64)
	case 2:
		return Int(1<<53 + 1)
	case 3:
		return Int(0)
	default:
		return Int(int64(u64fuzz(data)))
	}
}

func fuzzFloat(data []byte) Value {
	if len(data) == 0 {
		return Float(0)
	}
	switch data[0] % 5 {
	case 0:
		return Float(math.NaN())
	case 1:
		return Float(math.Copysign(0, -1))
	case 2:
		return Float(math.Inf(-1))
	case 3:
		return Float(1)
	default:
		return Float(math.Float64frombits(u64fuzz(data)))
	}
}

func u64fuzz(data []byte) uint64 {
	var u uint64
	for i := 0; i < len(data) && i < 8; i++ {
		u |= uint64(data[i]) << (8 * i)
	}
	return u
}

func drop(data []byte, n int) []byte {
	if n > len(data) {
		return nil
	}
	return data[n:]
}

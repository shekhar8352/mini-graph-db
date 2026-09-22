package value

import "math"

// compareNumber orders the numeric family.
// NaN is greater than every non-NaN, and every NaN equals every other NaN.
// −0 equals +0. An int and a float compare by exact numeric value, not by
// converting the int through float64 (which would collapse integers above 2^53).
func compareNumber(a, b Value) int {
	switch {
	case a.kind == KindInt && b.kind == KindInt:
		return cmpInt64(a.i, b.i)
	case a.kind == KindFloat && b.kind == KindFloat:
		return compareFloat(a.f, b.f)
	case a.kind == KindInt && b.kind == KindFloat:
		return compareIntFloat(a.i, b.f)
	default:
		return -compareIntFloat(b.i, a.f)
	}
}

func compareFloat(a, b float64) int {
	aNaN, bNaN := math.IsNaN(a), math.IsNaN(b)
	if aNaN || bNaN {
		if aNaN && bNaN {
			return 0
		}
		if aNaN {
			return 1
		}
		return -1
	}
	// IEEE comparison already treats −0 and +0 as equal.
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// compareIntFloat returns the order of exact integer i relative to float f.
func compareIntFloat(i int64, f float64) int {
	if math.IsNaN(f) {
		return -1
	}
	if math.IsInf(f, 1) {
		return -1
	}
	if math.IsInf(f, -1) {
		return 1
	}
	if f == 0 {
		return cmpInt64(i, 0)
	}
	// 2^63 is the smallest magnitude that does not fit in int64 on the positive side.
	const two63 = 9223372036854775808.0
	if f >= two63 {
		return -1
	}
	if f < float64(math.MinInt64) {
		return 1
	}
	trunc := int64(f)
	if f >= 0 {
		if i < trunc {
			return -1
		}
		if i > trunc {
			return 1
		}
		if float64(trunc) != f {
			return -1
		}
		return 0
	}
	if float64(trunc) == f {
		return cmpInt64(i, trunc)
	}
	// Non-integer negative: floor = trunc-1, and floor < f < trunc.
	// trunc cannot be MinInt64 here: the only float that converts to MinInt64
	// is MinInt64 itself, which was handled as exact.
	floor := trunc - 1
	if i <= floor {
		return -1
	}
	return 1
}

// floatIsExactInt64 reports whether f is an integer in the int64 range.
// −0 is treated as 0.
func floatIsExactInt64(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f == 0 {
		return 0, true
	}
	const two63 = 9223372036854775808.0
	if f >= two63 || f < float64(math.MinInt64) {
		return 0, false
	}
	i := int64(f)
	if float64(i) != f {
		return 0, false
	}
	return i, true
}

// splitIntForKey encodes an int64 onto the float64 number line.
// Exact integers return off == 0. Integers that fall in a float gap return
// the greatest float strictly below i and a positive offset of i-int(lower).
// The offset fits in 16 bits for every int64 (the float ulp at 2^63 is 2048).
func splitIntForKey(i int64) (lower float64, off uint16) {
	f := float64(i)
	if got, ok := floatIsExactInt64(f); ok && got == i {
		return canonicalFloat(f), 0
	}
	if compareIntFloat(i, f) < 0 {
		lower = math.Nextafter(f, math.Inf(-1))
	} else {
		lower = f
	}
	li, ok := floatIsExactInt64(lower)
	if !ok {
		// Unreachable for int64 inputs: gaps only exist where every float is integral.
		return canonicalFloat(f), 0
	}
	delta := i - li
	if delta <= 0 || delta > 65535 {
		return canonicalFloat(f), 0
	}
	return canonicalFloat(lower), uint16(delta)
}

func canonicalFloat(f float64) float64 {
	if math.IsNaN(f) {
		return math.NaN()
	}
	if f == 0 {
		return 0
	}
	return f
}

// orderedFloat maps a float onto uint64 so numeric order matches unsigned order.
// −0 is canonicalized to +0 and every NaN maps to one payload, above +Inf.
func orderedFloat(f float64) uint64 {
	f = canonicalFloat(f)
	if math.IsNaN(f) {
		f = math.NaN()
	}
	bits := math.Float64bits(f)
	if bits&(1<<63) != 0 {
		return ^bits
	}
	return bits ^ (1 << 63)
}

func unorderedFloat(bits uint64) float64 {
	if bits&(1<<63) != 0 {
		bits ^= 1 << 63
	} else {
		bits = ^bits
	}
	return math.Float64frombits(bits)
}

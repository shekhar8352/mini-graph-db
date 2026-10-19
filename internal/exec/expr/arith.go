package expr

import (
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func inv(msg string) error { return gerr.New(gerr.InvalidArgument, msg) }

func nan() float64 { return math.NaN() }
func inf(sign int) float64 {
	return math.Inf(sign)
}

func isNum(v value.Value) bool {
	return v.Kind() == value.KindInt || v.Kind() == value.KindFloat
}

func negate(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindInt:
		i, _ := v.IntValue()
		if i == math.MinInt64 {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(-i), nil
	case value.KindFloat:
		f, _ := v.FloatValue()
		return value.Float(-f), nil
	default:
		return value.Null(), inv("unary - expects a number")
	}
}

func compareOp(op string, l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	if op != "=" && op != "<>" && l.Kind() == value.KindBool && r.Kind() == value.KindBool {
		return value.Null(), nil
	}
	if !sameFamily(l, r) {
		return value.Null(), nil
	}
	c := value.Compare(l, r)
	var ok bool
	switch op {
	case "=":
		ok = c == 0
	case "<>":
		ok = c != 0
	case "<":
		ok = c < 0
	case "<=":
		ok = c <= 0
	case ">":
		ok = c > 0
	case ">=":
		ok = c >= 0
	default:
		return value.Null(), inv("unknown comparison")
	}
	return value.Bool(ok), nil
}

func sameFamily(l, r value.Value) bool {
	if l.Kind() == r.Kind() {
		return true
	}
	return isNum(l) && isNum(r)
}

func evalIn(l, r value.Value) (value.Value, error) {
	if r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	elems, ok := r.ListValue()
	if !ok {
		return value.Null(), inv("IN expects a list")
	}
	sawNull := false
	for _, el := range elems {
		eq, err := compareOp("=", l, el)
		if err != nil {
			return value.Null(), err
		}
		if isTrue(eq) {
			return value.Bool(true), nil
		}
		if eq.Kind() == value.KindNull {
			sawNull = true
		}
	}
	if sawNull {
		return value.Null(), nil
	}
	return value.Bool(false), nil
}

func strPred(op string, l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	ls, okL := l.StringValue()
	rs, okR := r.StringValue()
	if !okL || !okR {
		return value.Null(), inv(op + " expects strings")
	}
	var ok bool
	switch op {
	case "STARTS WITH":
		ok = strings.HasPrefix(ls, rs)
	case "ENDS WITH":
		ok = strings.HasSuffix(ls, rs)
	case "CONTAINS":
		ok = strings.Contains(ls, rs)
	default:
		return value.Null(), inv("unknown string predicate")
	}
	return value.Bool(ok), nil
}

func arith(op string, l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	switch op {
	case "+":
		return add(l, r)
	case "-":
		return sub(l, r)
	case "*":
		return mul(l, r)
	case "/":
		return div(l, r)
	case "%":
		return mod(l, r)
	case "^":
		return pow(l, r)
	default:
		return value.Null(), inv("unknown operator")
	}
}

func add(l, r value.Value) (value.Value, error) {
	if isNum(l) && isNum(r) {
		return addNum(l, r)
	}
	if l.Kind() == value.KindString && r.Kind() == value.KindString {
		ls, _ := l.StringValue()
		rs, _ := r.StringValue()
		return value.String(ls + rs), nil
	}
	if l.Kind() == value.KindList && r.Kind() == value.KindList {
		a, _ := l.ListValue()
		b, _ := r.ListValue()
		return value.List(append(a, b...)...), nil
	}
	if (l.Kind() == value.KindDate || l.Kind() == value.KindDateTime || l.Kind() == value.KindDuration) && r.Kind() == value.KindDuration {
		return addDur(l, r)
	}
	return value.Null(), inv("invalid +")
}

func sub(l, r value.Value) (value.Value, error) {
	if isNum(l) && isNum(r) {
		return subNum(l, r)
	}
	if l.Kind() == value.KindDuration && r.Kind() == value.KindDuration {
		return subDur(l, r)
	}
	if l.Kind() == value.KindDate && r.Kind() == value.KindDate {
		return dateDiff(l, r)
	}
	if l.Kind() == value.KindDateTime && r.Kind() == value.KindDateTime {
		return timeDiff(l, r)
	}
	return value.Null(), inv("invalid -")
}

func mul(l, r value.Value) (value.Value, error) {
	if isNum(l) && isNum(r) {
		return mulNum(l, r)
	}
	if l.Kind() == value.KindDuration && r.Kind() == value.KindInt {
		return scaleDur(l, r, false)
	}
	if r.Kind() == value.KindDuration && l.Kind() == value.KindInt {
		return scaleDur(r, l, false)
	}
	if (l.Kind() == value.KindDuration && r.Kind() == value.KindFloat) || (r.Kind() == value.KindDuration && l.Kind() == value.KindFloat) {
		return value.Null(), inv("duration * float")
	}
	return value.Null(), inv("invalid *")
}

func div(l, r value.Value) (value.Value, error) {
	if isNum(l) && isNum(r) {
		return divNum(l, r)
	}
	if l.Kind() == value.KindDuration && r.Kind() == value.KindInt {
		return scaleDur(l, r, true)
	}
	return value.Null(), inv("invalid /")
}

func addNum(l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindInt && r.Kind() == value.KindInt {
		a, _ := l.IntValue()
		b, _ := r.IntValue()
		c, ov := addInt(a, b)
		if ov {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(c), nil
	}
	return value.Float(asFloat(l) + asFloat(r)), nil
}

func subNum(l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindInt && r.Kind() == value.KindInt {
		a, _ := l.IntValue()
		b, _ := r.IntValue()
		c, ov := subInt(a, b)
		if ov {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(c), nil
	}
	return value.Float(asFloat(l) - asFloat(r)), nil
}

func mulNum(l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindInt && r.Kind() == value.KindInt {
		a, _ := l.IntValue()
		b, _ := r.IntValue()
		c, ov := mulInt(a, b)
		if ov {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(c), nil
	}
	return value.Float(asFloat(l) * asFloat(r)), nil
}

func divNum(l, r value.Value) (value.Value, error) {
	if l.Kind() == value.KindInt && r.Kind() == value.KindInt {
		a, _ := l.IntValue()
		b, _ := r.IntValue()
		if b == 0 {
			return value.Null(), inv("division by zero")
		}
		if a == math.MinInt64 && b == -1 {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(a / b), nil
	}
	rf := asFloat(r)
	if rf == 0 {
		return value.Null(), inv("division by zero")
	}
	return value.Float(asFloat(l) / rf), nil
}

func mod(l, r value.Value) (value.Value, error) {
	if l.Kind() != value.KindInt || r.Kind() != value.KindInt {
		return value.Null(), inv("% expects integers")
	}
	a, _ := l.IntValue()
	b, _ := r.IntValue()
	if b == 0 {
		return value.Null(), inv("division by zero")
	}
	if a == math.MinInt64 && b == -1 {
		return value.Int(0), nil
	}
	return value.Int(a % b), nil
}

func pow(l, r value.Value) (value.Value, error) {
	if !isNum(l) || !isNum(r) {
		return value.Null(), inv("^ expects numbers")
	}
	bf, ef := asFloat(l), asFloat(r)
	if isZeroNum(l) && expNegative(r) {
		return value.Null(), inv("zero to a negative power")
	}
	out := math.Pow(bf, ef)
	if math.IsNaN(out) && !math.IsNaN(bf) && !math.IsNaN(ef) {
		return value.Null(), inv("power is not a real number")
	}
	return value.Float(out), nil
}

func isZeroNum(v value.Value) bool {
	if v.Kind() == value.KindInt {
		i, _ := v.IntValue()
		return i == 0
	}
	f, _ := v.FloatValue()
	return f == 0
}

func expNegative(v value.Value) bool {
	if v.Kind() == value.KindInt {
		i, _ := v.IntValue()
		return i < 0
	}
	f, _ := v.FloatValue()
	return f < 0 && !math.IsNaN(f)
}

func asFloat(v value.Value) float64 {
	if v.Kind() == value.KindInt {
		i, _ := v.IntValue()
		return float64(i)
	}
	f, _ := v.FloatValue()
	return f
}

func addInt(a, b int64) (int64, bool) {
	c := a + b
	if (b > 0 && c < a) || (b < 0 && c > a) {
		return 0, true
	}
	return c, false
}

func subInt(a, b int64) (int64, bool) {
	c := a - b
	if (b > 0 && c > a) || (b < 0 && c < a) {
		return 0, true
	}
	return c, false
}

func mulInt(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return 0, true
	}
	c := a * b
	if c/b != a {
		return 0, true
	}
	return c, false
}

func addDur(l, r value.Value) (value.Value, error) {
	months, days, nanos, _ := r.DurationValue()
	switch l.Kind() {
	case value.KindDuration:
		lm, ld, ln, _ := l.DurationValue()
		m, ov1 := addInt(lm, months)
		d, ov2 := addInt(ld, days)
		n, ov3 := addInt(ln, nanos)
		if ov1 || ov2 || ov3 {
			return value.Null(), inv("integer overflow")
		}
		return value.Duration(m, d, n), nil
	case value.KindDate:
		if nanos != 0 {
			return value.Null(), inv("date plus a duration with nanoseconds")
		}
		y, m, d, _ := l.DateValue()
		ny, nm, nd, err := addYMD(y, int(m), d, months, days)
		if err != nil {
			return value.Null(), err
		}
		out, err := value.Date(ny, time.Month(nm), nd)
		if err != nil {
			return value.Null(), inv("date out of range")
		}
		return out, nil
	case value.KindDateTime:
		return addDateTime(l, months, days, nanos)
	default:
		return value.Null(), inv("invalid +")
	}
}

func subDur(l, r value.Value) (value.Value, error) {
	lm, ld, ln, _ := l.DurationValue()
	rm, rd, rn, _ := r.DurationValue()
	m, ov1 := subInt(lm, rm)
	d, ov2 := subInt(ld, rd)
	n, ov3 := subInt(ln, rn)
	if ov1 || ov2 || ov3 {
		return value.Null(), inv("integer overflow")
	}
	return value.Duration(m, d, n), nil
}

func dateDiff(l, r value.Value) (value.Value, error) {
	ly, lm, ld, _ := l.DateValue()
	ry, rm, rd, _ := r.DateValue()
	a := time.Date(ly, lm, ld, 0, 0, 0, 0, time.UTC)
	b := time.Date(ry, rm, rd, 0, 0, 0, 0, time.UTC)
	days := int64(a.Sub(b) / (24 * time.Hour))
	return value.Duration(0, days, 0), nil
}

func timeDiff(l, r value.Value) (value.Value, error) {
	a, _, _, _ := l.DateTimeValue()
	b, _, _, _ := r.DateTimeValue()
	an, bn := a.UnixNano(), b.UnixNano()
	d, ov := subInt(an, bn)
	if ov {
		return value.Null(), inv("integer overflow")
	}
	return value.Duration(0, 0, d), nil
}

func scaleDur(d, k value.Value, div bool) (value.Value, error) {
	m, day, n, _ := d.DurationValue()
	f, _ := k.IntValue()
	if div {
		if f == 0 {
			return value.Null(), inv("division by zero")
		}
		return value.Duration(m/f, day/f, n/f), nil
	}
	mm, ov1 := mulInt(m, f)
	dd, ov2 := mulInt(day, f)
	nn, ov3 := mulInt(n, f)
	if ov1 || ov2 || ov3 {
		return value.Null(), inv("integer overflow")
	}
	return value.Duration(mm, dd, nn), nil
}

func addYMD(y, m, d int, months, days int64) (int, int, int, error) {
	ny, nm, err := shiftMonths(y, m, months)
	if err != nil {
		return 0, 0, 0, err
	}
	dim := time.Date(ny, time.Month(nm)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > dim {
		d = dim
	}
	if days > math.MaxInt || days < math.MinInt {
		return 0, 0, 0, inv("integer overflow")
	}
	t := time.Date(ny, time.Month(nm), d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
	if t.Year() < -9999 || t.Year() > 9999 {
		return 0, 0, 0, inv("date out of range")
	}
	return t.Year(), int(t.Month()), t.Day(), nil
}

func shiftMonths(y, m int, delta int64) (int, int, error) {
	total, ov := addInt(int64(y)*12+int64(m-1), delta)
	if ov {
		return 0, 0, inv("integer overflow")
	}
	ny := total / 12
	nm := total % 12
	if nm < 0 {
		nm += 12
		ny--
	}
	if ny < -9999 || ny > 9999 {
		return 0, 0, inv("date out of range")
	}
	return int(ny), int(nm) + 1, nil
}

func addDateTime(v value.Value, months, days, nanos int64) (value.Value, error) {
	local, zoneSet, off, ok := localTime(v)
	if !ok {
		return value.Null(), inv("invalid datetime")
	}
	y, m, d, err := addYMD(local.Year(), int(local.Month()), local.Day(), months, days)
	if err != nil {
		return value.Null(), err
	}
	tod := int64(local.Hour())*int64(time.Hour) + int64(local.Minute())*int64(time.Minute) + int64(local.Second())*int64(time.Second) + int64(local.Nanosecond())
	newTod, carry, err := addClock(tod, nanos)
	if err != nil {
		return value.Null(), err
	}
	if carry != 0 {
		y, m, d, err = addYMD(y, m, d, 0, carry)
		if err != nil {
			return value.Null(), err
		}
	}
	sec := newTod / int64(time.Second)
	ns := newTod % int64(time.Second)
	hh := sec / 3600
	mm := (sec % 3600) / 60
	ss := sec % 60
	clock := time.Date(y, time.Month(m), d, int(hh), int(mm), int(ss), int(ns), time.UTC)
	utc := clock
	if zoneSet {
		utc = clock.Add(-time.Duration(off) * time.Second)
	}
	var out value.Value
	if zoneSet {
		out, err = value.DateTimeOffset(utc, off)
	} else {
		out, err = value.DateTimeUTC(utc)
	}
	if err != nil {
		return value.Null(), inv("datetime out of range")
	}
	return out, nil
}

func localTime(v value.Value) (time.Time, bool, int, bool) {
	inst, zoneSet, off, ok := v.DateTimeValue()
	if !ok {
		return time.Time{}, false, 0, false
	}
	if !zoneSet {
		return inst, false, 0, true
	}
	return inst.Add(time.Duration(off) * time.Second), true, off, true
}

func addClock(tod, delta int64) (int64, int64, error) {
	const dayNs = int64(24 * time.Hour)
	s := new(big.Int).Add(big.NewInt(tod), big.NewInt(delta))
	day := big.NewInt(dayNs)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(s, day, r)
	if r.Sign() < 0 {
		r.Add(r, day)
		q.Sub(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, 0, inv("integer overflow")
	}
	return r.Int64(), q.Int64(), nil
}

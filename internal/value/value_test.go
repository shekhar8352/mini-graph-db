package value

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestKindsRoundTripRecord(t *testing.T) {
	samples := allSamples(t)
	for _, v := range samples {
		enc := EncodeRecord(v)
		got, err := DecodeRecord(enc)
		if err != nil {
			t.Fatalf("decode %s: %v", v, err)
		}
		if got.Kind() != v.Kind() {
			t.Fatalf("kind %s decoded as %s", v.Kind(), got.Kind())
		}
		if !Equal(v, got) {
			t.Fatalf("record round trip %s -> %s", v, got)
		}
		if !bytes.Equal(enc, EncodeRecord(got)) {
			t.Fatalf("record encoding unstable for %s", v)
		}
	}
}

func TestKindsRoundTripKey(t *testing.T) {
	for _, v := range allSamples(t) {
		enc := EncodeKey(v)
		got, err := DecodeKey(enc)
		if err != nil {
			t.Fatalf("decode key %s: %v", v, err)
		}
		if !Equal(v, got) {
			t.Fatalf("key round trip %s -> %s", v, got)
		}
		if !bytes.Equal(enc, EncodeKey(got)) {
			t.Fatalf("key encoding not canonical for %s -> %s", v, got)
		}
	}
}

func TestKeyOrderMatchesCompare(t *testing.T) {
	samples := allSamples(t)
	for i, a := range samples {
		for j, b := range samples {
			c := Compare(a, b)
			bc := bytes.Compare(EncodeKey(a), EncodeKey(b))
			if sign(c) != sign(bc) {
				t.Fatalf("order mismatch [%d,%d] %s vs %s: compare %d bytes %d", i, j, a, b, c, bc)
			}
			if c == 0 && Hash(a) != Hash(b) {
				t.Fatalf("hash mismatch for equal %s and %s", a, b)
			}
		}
	}
}

func TestNumericCrossType(t *testing.T) {
	if Compare(Int(1), Float(1)) != 0 {
		t.Fatal("1 != 1.0")
	}
	if !bytes.Equal(EncodeKey(Int(1)), EncodeKey(Float(1))) {
		t.Fatal("int 1 and float 1.0 keys differ")
	}
	negZero := Float(math.Copysign(0, -1))
	if Compare(negZero, Float(0)) != 0 || Compare(negZero, Int(0)) != 0 {
		t.Fatal("-0 should equal 0")
	}
	if !bytes.Equal(EncodeKey(negZero), EncodeKey(Int(0))) {
		t.Fatal("-0 key should match 0")
	}
	nan1 := Float(math.Float64frombits(0x7ff8000000000001))
	nan2 := Float(math.Float64frombits(0x7ff8000000000002))
	if Compare(nan1, nan2) != 0 || Hash(nan1) != Hash(nan2) {
		t.Fatal("NaNs should be equal")
	}
	if Compare(Float(math.Inf(1)), nan1) >= 0 {
		t.Fatal("NaN should be greater than +Inf")
	}
	if Compare(Int(1), nan1) >= 0 {
		t.Fatal("NaN should be greater than 1")
	}
	big := int64(1<<53) + 1
	if Compare(Int(big), Float(float64(big))) == 0 {
		t.Fatal("2^53+1 must not collapse to its float64 rounding")
	}
	if Compare(Int(big), Float(float64(1<<53))) <= 0 {
		t.Fatal("2^53+1 should be greater than 2^53")
	}
	next := float64(int64(1<<53) + 2)
	if Compare(Int(big), Float(next)) >= 0 {
		t.Fatal("2^53+1 should be less than 2^53+2")
	}
	if Compare(Int(math.MaxInt64), Float(float64(math.MaxInt64))) >= 0 {
		t.Fatal("MaxInt64 should be less than the float it rounds to")
	}
	if Compare(Int(math.MinInt64), Float(float64(math.MinInt64))) != 0 {
		t.Fatal("MinInt64 is an exact float")
	}
}

func TestThreeValuedLogic(t *testing.T) {
	null := Null()
	eq, err := ApplyOp("=", Int(1), null)
	if err != nil || eq.Kind() != KindNull {
		t.Fatalf("= null: %s %v", eq, err)
	}
	ne, err := ApplyOp("!=", null, Int(1))
	if err != nil || ne.Kind() != KindNull {
		t.Fatalf("!= null: %s %v", ne, err)
	}
	is, err := ApplyOp("IS NULL", null, Int(1))
	if err != nil || !mustBool(t, is) {
		t.Fatalf("IS NULL: %s %v", is, err)
	}
	isNot, err := ApplyOp("IS NOT NULL", Int(1), null)
	if err != nil || !mustBool(t, isNot) {
		t.Fatalf("IS NOT NULL: %s", isNot)
	}
	got, err := ApplyOp("=", Int(1), Float(1))
	if err != nil || !mustBool(t, got) {
		t.Fatal("1 = 1.0")
	}
	incomp, err := ApplyOp(">", String("a"), Int(1))
	if err != nil || incomp.Kind() != KindNull {
		t.Fatalf("incomparable: %s", incomp)
	}
	notNull, err := Not(null)
	if err != nil || notNull.Kind() != KindNull {
		t.Fatal("NOT null")
	}
	and, err := And(Bool(false), null)
	if err != nil || mustBool(t, and) {
		t.Fatal("false AND null should be false")
	}
	or, err := Or(Bool(true), null)
	if err != nil || !mustBool(t, or) {
		t.Fatal("true OR null should be true")
	}
	or2, err := Or(null, Bool(false))
	if err != nil || or2.Kind() != KindNull {
		t.Fatal("null OR false should be null")
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	for _, v := range allSamples(t) {
		s := Format(v)
		got, err := Parse(s)
		if err != nil {
			t.Fatalf("parse %s (%s): %v", s, v.Kind(), err)
		}
		if got.Kind() != v.Kind() || !Equal(v, got) {
			t.Fatalf("format round trip %s -> %s -> %s", v, s, got)
		}
	}
}

func TestLiteralExamples(t *testing.T) {
	d, err := Parse(`date("2025-01-31")`)
	if err != nil {
		t.Fatal(err)
	}
	y, m, day, ok := d.DateValue()
	if !ok || y != 2025 || m != time.January || day != 31 {
		t.Fatalf("date: %d %s %d", y, m, day)
	}
	epoch, err := Date(1970, time.January, 1)
	if err != nil || epoch.i != 0 {
		t.Fatalf("epoch days %d %v", epoch.i, err)
	}
	prev, err := Date(1969, time.December, 31)
	if err != nil || prev.i != -1 {
		t.Fatalf("day before epoch %d", prev.i)
	}
	dt, err := Parse(`datetime("2025-01-31T12:00:00+05:30")`)
	if err != nil {
		t.Fatal(err)
	}
	inst, zone, off, ok := dt.DateTimeValue()
	if !ok || !zone || off != 5*3600+30*60 {
		t.Fatalf("offset %v %v %d", zone, ok, off)
	}
	if inst.UTC().Hour() != 6 || inst.UTC().Minute() != 30 {
		t.Fatalf("instant %s", inst)
	}
	bare, err := Parse(`datetime("2025-01-31T12:00:00")`)
	if err != nil {
		t.Fatal(err)
	}
	_, zone, _, _ = bare.DateTimeValue()
	if zone {
		t.Fatal("bare datetime should have no zone")
	}
	dur, err := Parse(`duration("P1DT2H")`)
	if err != nil {
		t.Fatal(err)
	}
	months, days, nanos, ok := dur.DurationValue()
	if !ok || months != 0 || days != 1 || nanos != int64(2*time.Hour) {
		t.Fatalf("duration %d %d %d", months, days, nanos)
	}
	if Compare(Duration(1, 0, 0), Duration(0, 30, 0)) == 0 {
		t.Fatal("1 month must not equal 30 days")
	}
}

func TestFloatRecordPreservesBits(t *testing.T) {
	neg := Float(math.Copysign(0, -1))
	got, err := DecodeRecord(EncodeRecord(neg))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := got.FloatValue()
	if !math.Signbit(f) {
		t.Fatal("record encoding dropped the sign of zero")
	}
	payload := uint64(0x7ff8000000000002)
	nan := Float(math.Float64frombits(payload))
	got, err = DecodeRecord(EncodeRecord(nan))
	if err != nil {
		t.Fatal(err)
	}
	f, _ = got.FloatValue()
	if math.Float64bits(f) != payload {
		t.Fatalf("nan payload %x", math.Float64bits(f))
	}
}

func TestCompositeKeys(t *testing.T) {
	a := EncodeComposite(Int(1), String("b"))
	b := EncodeComposite(Int(1), String("c"))
	c := EncodeComposite(Int(2), String("a"))
	d := EncodeComposite(Int(1))
	if bytes.Compare(a, b) >= 0 || bytes.Compare(b, c) >= 0 {
		t.Fatal("composite order")
	}
	if bytes.Compare(d, a) >= 0 {
		t.Fatal("prefix composite should sort first")
	}
	vals, err := DecodeComposite(a)
	if err != nil || len(vals) != 2 || !Equal(vals[0], Int(1)) || !Equal(vals[1], String("b")) {
		t.Fatalf("decode composite %v %v", vals, err)
	}
}

func TestPathNotStorable(t *testing.T) {
	p, err := PathOf([]uint64{1, 2}, []uint64{9})
	if err != nil {
		t.Fatal(err)
	}
	if p.Storable() {
		t.Fatal("path should not be storable")
	}
	if !List(Int(1)).Storable() {
		t.Fatal("list of ints is storable")
	}
	if List(p).Storable() || Map(map[string]Value{"p": p}).Storable() {
		t.Fatal("nested path should not be storable")
	}
	if _, err := PathOf([]uint64{1}, []uint64{2}); err == nil {
		t.Fatal("expected invalid path")
	}
}

func TestRejectNewerRecord(t *testing.T) {
	_, err := DecodeRecord([]byte{9, 0})
	if err == nil {
		t.Fatal("expected version error")
	}
}

func TestFromAny(t *testing.T) {
	v, err := FromAny(map[string]any{"name": "Alice", "age": int64(30), "ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind() != KindMap {
		t.Fatal(v)
	}
	if _, err := FromAny(struct{}{}); err == nil {
		t.Fatal("expected unsupported type")
	}
}

func allSamples(t *testing.T) []Value {
	t.Helper()
	date, err := Date(2025, time.January, 31)
	if err != nil {
		t.Fatal(err)
	}
	dt, err := DateTimeUTC(time.Date(2025, 1, 31, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	dtz, err := DateTimeOffset(time.Date(2025, 1, 31, 6, 30, 0, 0, time.UTC), 5*3600+30*60)
	if err != nil {
		t.Fatal(err)
	}
	path, err := PathOf([]uint64{1, 3}, []uint64{2})
	if err != nil {
		t.Fatal(err)
	}
	emptyPath, err := PathOf(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return []Value{
		Null(),
		Bool(false),
		Bool(true),
		Int(0),
		Int(1),
		Int(-1),
		Int(math.MinInt64),
		Int(math.MaxInt64),
		Int(1<<53 + 1),
		Int(-(1<<53 + 1)),
		Int(1<<54 + 3),
		Float(0),
		Float(math.Copysign(0, -1)),
		Float(1),
		Float(1.5),
		Float(-2.25),
		Float(math.Inf(1)),
		Float(math.Inf(-1)),
		Float(math.NaN()),
		Float(math.Float64frombits(0x7ff8000000000002)),
		String(""),
		String("a"),
		String("a\x00b"),
		String("Alice"),
		Bytes(nil),
		Bytes([]byte{0, 1, 0xff}),
		date,
		dt,
		dtz,
		Duration(0, 0, 0),
		Duration(0, 1, int64(2*time.Hour)),
		Duration(1, 0, 0),
		Duration(0, 30, 0),
		Duration(-1, 2, -3),
		List(),
		List(Null(), Int(1), String("a")),
		List(Int(1)),
		List(Int(1), Int(2)),
		Map(nil),
		Map(map[string]Value{"b": Int(1), "a": String("x")}),
		Map(map[string]Value{"": Null(), "n": Int(1)}),
		NodeRef(1),
		NodeRef(math.MaxUint64),
		EdgeRef(2),
		emptyPath,
		path,
	}
}

func mustBool(t *testing.T, v Value) bool {
	t.Helper()
	b, ok := v.BoolValue()
	if !ok {
		t.Fatalf("not a bool: %s", v)
	}
	return b
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

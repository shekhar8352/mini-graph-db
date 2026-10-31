package server

import (
	"math"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestValueRoundTrip(t *testing.T) {
	dt, err := value.DateTimeOffset(time.Unix(0, 1_700_000_000_123456789).UTC(), -3600)
	if err != nil {
		t.Fatal(err)
	}
	date, err := value.Date(2026, time.October, 31)
	if err != nil {
		t.Fatal(err)
	}
	path, err := value.PathOf([]uint64{1, 2}, []uint64{9})
	if err != nil {
		t.Fatal(err)
	}
	samples := []value.Value{
		value.Null(),
		value.Bool(false),
		value.Int(-42),
		value.Float(math.Copysign(0, -1)),
		value.Float(math.NaN()),
		value.Float(math.Inf(1)),
		value.String(""),
		value.Bytes([]byte{0, 1, 255}),
		date,
		dt,
		value.Duration(1, -2, 3),
		value.List(value.Int(1), value.Null(), value.String("a")),
		value.Map(map[string]value.Value{"b": value.Int(2), "a": value.Bool(true)}),
		value.NodeRef(7),
		value.EdgeRef(8),
		path,
	}
	for _, in := range samples {
		msg, err := toProto(in)
		if err != nil {
			t.Fatalf("to %s: %v", in.Kind(), err)
		}
		out, err := fromProto(msg)
		if err != nil {
			t.Fatalf("from %s: %v", in.Kind(), err)
		}
		if in.Kind() == value.KindFloat {
			fb, _ := in.FloatValue()
			ob, _ := out.FloatValue()
			if math.Float64bits(fb) != math.Float64bits(ob) {
				t.Fatalf("float bits %x %x", math.Float64bits(fb), math.Float64bits(ob))
			}
			continue
		}
		if value.Compare(in, out) != 0 || in.Kind() != out.Kind() {
			t.Fatalf("%s: %s -> %s", in.Kind(), in, out)
		}
	}
}

func TestValueRejectsBadDate(t *testing.T) {
	msg, err := toProto(value.Int(1))
	if err != nil {
		t.Fatal(err)
	}
	msg.Kind = nil
	got, err := fromProto(msg)
	if err != nil || got.Kind() != value.KindNull {
		t.Fatalf("unset %v %v", got, err)
	}
}

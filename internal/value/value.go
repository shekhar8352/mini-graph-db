// Package value is the typed value model shared by storage, indexes, and queries.
//
// A Value is a tagged union, not an interface: the hot path switches on Kind
// and reads plain fields. Values are immutable after construction. Slices and
// maps returned by accessors are copies.
//
// Two encodings exist and they are not interchangeable:
//
//   - EncodeKey / DecodeKey is memcomparable. bytes.Compare on keys matches
//     Compare. Numerically equal ints and floats share one key, so key decoding
//     returns a canonical kind (an exact int64 float decodes as Int).
//   - EncodeRecord / DecodeRecord is compact and kind-preserving, including
//     the sign bit of −0 and NaN payloads. It is not order-preserving.
//
// The zero Value is Null.
package value

import (
	"fmt"
	"time"
)

// Kind identifies the active arm of a Value.
//
// The numeric values are the record-encoding tags. Do not renumber them;
// on-disk records (format version 1) store this byte.
type Kind uint8

// Record-encoding tags. These numbers are stored on disk; do not renumber them.
const (
	// KindNull is the unknown value. The zero Value has this kind.
	KindNull Kind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindBytes
	KindDate
	KindDateTime
	KindDuration
	KindList
	KindMap
	KindNode
	KindEdge
	KindPath
)

// String returns a stable lowercase name for k.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindBytes:
		return "bytes"
	case KindDate:
		return "date"
	case KindDateTime:
		return "datetime"
	case KindDuration:
		return "duration"
	case KindList:
		return "list"
	case KindMap:
		return "map"
	case KindNode:
		return "node"
	case KindEdge:
		return "edge"
	case KindPath:
		return "path"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// kv is one map entry. Keys are stored sorted by raw byte order.
type kv struct {
	key string
	val Value
}

// Value is one typed datum.
//
// Field use by kind:
//
//	Bool       b
//	Int        i
//	Float      f
//	String     s
//	Bytes      raw
//	Date       i (days since 1970-01-01, proleptic Gregorian)
//	DateTime   i (UTC unix nanoseconds), zoneSet, zoneOff (seconds east of UTC)
//	Duration   i (months), day, nsec
//	List       list
//	Map        kvs
//	Node, Edge u
//	Path       nodes, edges (len(edges) == len(nodes)-1, or both empty)
type Value struct {
	kind    Kind
	b       bool
	zoneSet bool
	zoneOff int32
	i       int64
	day     int64
	nsec    int64
	u       uint64
	f       float64
	s       string
	raw     []byte
	list    []Value
	kvs     []kv
	nodes   []uint64
	edges   []uint64
}

// Kind returns the active arm. The zero Value reports KindNull.
func (v Value) Kind() Kind { return v.kind }

// Storable reports whether v may be stored as a property.
// Path, and any list or map that contains a Path, is not storable.
func (v Value) Storable() bool {
	switch v.kind {
	case KindPath:
		return false
	case KindList:
		for _, el := range v.list {
			if !el.Storable() {
				return false
			}
		}
	case KindMap:
		for _, e := range v.kvs {
			if !e.val.Storable() {
				return false
			}
		}
	}
	return true
}

// Null returns a Null value.
func Null() Value { return Value{kind: KindNull} }

// Bool returns a boolean value.
func Bool(b bool) Value { return Value{kind: KindBool, b: b} }

// Int returns an int64 value.
func Int(i int64) Value { return Value{kind: KindInt, i: i} }

// Float returns a float64 value. NaN payloads and the sign of zero are kept.
func Float(f float64) Value { return Value{kind: KindFloat, f: f} }

// String returns a string value.
func String(s string) Value { return Value{kind: KindString, s: s} }

// Bytes returns a byte-string value. The slice is copied.
func Bytes(b []byte) Value {
	return Value{kind: KindBytes, raw: append([]byte(nil), b...)}
}

// List returns a list value. Elements are copied.
func List(elems ...Value) Value {
	return Value{kind: KindList, list: append([]Value(nil), elems...)}
}

// Map returns a map value. Entries are copied and sorted by key.
// A nil map is an empty map.
func Map(m map[string]Value) Value {
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, kv{key: k, val: v})
	}
	sortKVs(kvs)
	return Value{kind: KindMap, kvs: kvs}
}

// Date returns a calendar date in the proleptic Gregorian calendar.
// The year, month, and day must form a real date.
func Date(year int, month time.Month, day int) (Value, error) {
	if !validDate(year, int(month), day) {
		return Value{}, fmt.Errorf("value: invalid date %04d-%02d-%02d", year, month, day)
	}
	days, err := daysFromCivil(year, int(month), day)
	if err != nil {
		return Value{}, err
	}
	return Value{kind: KindDate, i: days}, nil
}

// DateTimeUTC returns a UTC instant with no zone offset recorded.
// The instant must fit in an int64 nanosecond count (about years 1678–2262).
func DateTimeUTC(t time.Time) (Value, error) {
	n, err := unixNanos(t)
	if err != nil {
		return Value{}, err
	}
	return Value{kind: KindDateTime, i: n}, nil
}

// DateTimeOffset returns a UTC instant plus a fixed zone offset in seconds
// east of UTC. The offset must lie in [−18h, +18h].
func DateTimeOffset(t time.Time, offsetSec int) (Value, error) {
	if offsetSec < -18*3600 || offsetSec > 18*3600 {
		return Value{}, fmt.Errorf("value: zone offset %d out of range", offsetSec)
	}
	n, err := unixNanos(t)
	if err != nil {
		return Value{}, err
	}
	return Value{kind: KindDateTime, i: n, zoneSet: true, zoneOff: int32(offsetSec)}, nil
}

// Duration returns a duration of months, days, and nanoseconds.
// The three components are independent: one month does not equal any number
// of days. Mixed signs are allowed.
func Duration(months, days, nanos int64) Value {
	return Value{kind: KindDuration, i: months, day: days, nsec: nanos}
}

// NodeRef returns a reference to a node id.
func NodeRef(id uint64) Value { return Value{kind: KindNode, u: id} }

// EdgeRef returns a reference to an edge id.
func EdgeRef(id uint64) Value { return Value{kind: KindEdge, u: id} }

// PathOf returns a path. edges must be empty when nodes is empty, and
// otherwise len(edges) must be len(nodes)-1. Slices are copied.
func PathOf(nodes, edges []uint64) (Value, error) {
	if len(nodes) == 0 {
		if len(edges) != 0 {
			return Value{}, fmt.Errorf("value: path has edges but no nodes")
		}
		return Value{kind: KindPath}, nil
	}
	if len(edges) != len(nodes)-1 {
		return Value{}, fmt.Errorf("value: path has %d nodes and %d edges", len(nodes), len(edges))
	}
	return Value{
		kind:  KindPath,
		nodes: append([]uint64(nil), nodes...),
		edges: append([]uint64(nil), edges...),
	}, nil
}

// BoolValue returns the boolean payload.
func (v Value) BoolValue() (bool, bool) {
	if v.kind != KindBool {
		return false, false
	}
	return v.b, true
}

// IntValue returns the int64 payload.
func (v Value) IntValue() (int64, bool) {
	if v.kind != KindInt {
		return 0, false
	}
	return v.i, true
}

// FloatValue returns the float64 payload.
func (v Value) FloatValue() (float64, bool) {
	if v.kind != KindFloat {
		return 0, false
	}
	return v.f, true
}

// StringValue returns the string payload.
func (v Value) StringValue() (string, bool) {
	if v.kind != KindString {
		return "", false
	}
	return v.s, true
}

// BytesValue returns a copy of the byte payload.
func (v Value) BytesValue() ([]byte, bool) {
	if v.kind != KindBytes {
		return nil, false
	}
	return append([]byte(nil), v.raw...), true
}

// ListValue returns a copy of the list elements.
func (v Value) ListValue() ([]Value, bool) {
	if v.kind != KindList {
		return nil, false
	}
	return append([]Value(nil), v.list...), true
}

// MapValue returns a copy of the map.
func (v Value) MapValue() (map[string]Value, bool) {
	if v.kind != KindMap {
		return nil, false
	}
	out := make(map[string]Value, len(v.kvs))
	for _, e := range v.kvs {
		out[e.key] = e.val
	}
	return out, true
}

// DateValue returns the calendar date.
func (v Value) DateValue() (year int, month time.Month, day int, ok bool) {
	if v.kind != KindDate {
		return 0, 0, 0, false
	}
	y, m, d := civilFromDays(v.i)
	return y, time.Month(m), d, true
}

// DateTimeValue returns the UTC instant, whether a zone offset was recorded,
// and that offset in seconds east of UTC.
func (v Value) DateTimeValue() (instant time.Time, zoneSet bool, offsetSec int, ok bool) {
	if v.kind != KindDateTime {
		return time.Time{}, false, 0, false
	}
	return time.Unix(0, v.i).UTC(), v.zoneSet, int(v.zoneOff), true
}

// DurationValue returns months, days, and nanoseconds.
func (v Value) DurationValue() (months, days, nanos int64, ok bool) {
	if v.kind != KindDuration {
		return 0, 0, 0, false
	}
	return v.i, v.day, v.nsec, true
}

// NodeID returns the referenced node id.
func (v Value) NodeID() (uint64, bool) {
	if v.kind != KindNode {
		return 0, false
	}
	return v.u, true
}

// EdgeID returns the referenced edge id.
func (v Value) EdgeID() (uint64, bool) {
	if v.kind != KindEdge {
		return 0, false
	}
	return v.u, true
}

// PathValue returns copies of the node and edge id sequences.
func (v Value) PathValue() (nodes, edges []uint64, ok bool) {
	if v.kind != KindPath {
		return nil, nil, false
	}
	return append([]uint64(nil), v.nodes...), append([]uint64(nil), v.edges...), true
}

// String formats v the same way Format does, so fmt uses the literal syntax.
func (v Value) String() string { return Format(v) }

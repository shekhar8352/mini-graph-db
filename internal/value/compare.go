package value

import "fmt"

// Compare returns the total order of a and b: −1, 0, or +1.
//
// Order of kinds:
//
//	Null < Bool < Int/Float < String < Bytes < Date < DateTime < Duration < List < Map < Node < Edge < Path
//
// Int and Float are one numeric family: 1 and 1.0 compare equal, and a
// non-integral float sorts between the surrounding integers. −0.0 equals +0.0.
// Every NaN equals every other NaN and is greater than every other number,
// including +Inf. Null equals Null (this is ordering equality, not the
// three-valued predicate "=").
//
// Bool is false < true. Strings and bytes use unsigned byte order, not a
// linguistic collation. Dates are chronological. DateTimes order by UTC
// instant, then by zone: an unspecified offset sorts before any recorded
// offset, including zero. Durations order by (months, days, nanos), which is
// not elapsed time: one month and thirty days are not equal. Lists and maps
// are lexicographic; a proper prefix sorts first. Maps walk keys in byte order.
// Node and edge refs order by id. Paths walk node, edge, node, … and a proper
// prefix sorts first.
func Compare(a, b Value) int {
	oa, ob := kindRank(a.kind), kindRank(b.kind)
	if oa != ob {
		return cmpInt(oa, ob)
	}
	switch a.kind {
	case KindNull:
		return 0
	case KindBool:
		return cmpBool(a.b, b.b)
	case KindInt, KindFloat:
		return compareNumber(a, b)
	case KindString:
		return cmpString(a.s, b.s)
	case KindBytes:
		return cmpBytes(a.raw, b.raw)
	case KindDate:
		return cmpInt64(a.i, b.i)
	case KindDateTime:
		if c := cmpInt64(a.i, b.i); c != 0 {
			return c
		}
		if a.zoneSet != b.zoneSet {
			if a.zoneSet {
				return 1
			}
			return -1
		}
		if !a.zoneSet {
			return 0
		}
		return cmpInt32(a.zoneOff, b.zoneOff)
	case KindDuration:
		if c := cmpInt64(a.i, b.i); c != 0 {
			return c
		}
		if c := cmpInt64(a.day, b.day); c != 0 {
			return c
		}
		return cmpInt64(a.nsec, b.nsec)
	case KindList:
		return compareLists(a.list, b.list)
	case KindMap:
		return compareMaps(a.kvs, b.kvs)
	case KindNode, KindEdge:
		return cmpUint64(a.u, b.u)
	case KindPath:
		return comparePaths(a, b)
	default:
		return cmpInt(int(a.kind), int(b.kind))
	}
}

// Equal reports ordering equality: Equal(a, b) is Compare(a, b) == 0.
// Null equals Null. Numerically equal ints and floats are equal. Distinct
// NaN payloads are equal. −0.0 equals +0.0.
func Equal(a, b Value) bool { return Compare(a, b) == 0 }

// ApplyOp evaluates a comparison predicate.
//
// "=", "!=", "<", "<=", ">", ">=" return Null when either side is Null, and
// when the kinds are not comparable (different kinds, unless both are numeric).
// "IS NULL" and "IS NOT NULL" ignore the right operand and always return Bool.
//
// Bool inequalities are allowed here (false < true). The legacy query layer
// still rejects them; ORDER BY uses Compare directly.
func ApplyOp(op string, left, right Value) (Value, error) {
	switch op {
	case "IS NULL":
		return Bool(left.kind == KindNull), nil
	case "IS NOT NULL":
		return Bool(left.kind != KindNull), nil
	case "=", "!=", "<", "<=", ">", ">=":
	default:
		return Value{}, fmt.Errorf("value: unknown operator %s", op)
	}
	if left.kind == KindNull || right.kind == KindNull {
		return Null(), nil
	}
	if !comparableKinds(left.kind, right.kind) {
		return Null(), nil
	}
	c := Compare(left, right)
	var ok bool
	switch op {
	case "=":
		ok = c == 0
	case "!=":
		ok = c != 0
	case "<":
		ok = c < 0
	case "<=":
		ok = c <= 0
	case ">":
		ok = c > 0
	case ">=":
		ok = c >= 0
	}
	return Bool(ok), nil
}

// Not is three-valued: NOT true is false, NOT false is true, NOT null is null.
func Not(v Value) (Value, error) {
	switch v.kind {
	case KindNull:
		return Null(), nil
	case KindBool:
		return Bool(!v.b), nil
	default:
		return Value{}, fmt.Errorf("value: NOT expects bool or null, got %s", v.kind)
	}
}

// And is three-valued. false AND x is false; true AND x is x; null AND null is null.
func And(a, b Value) (Value, error) {
	if err := expectLogic(a); err != nil {
		return Value{}, err
	}
	if err := expectLogic(b); err != nil {
		return Value{}, err
	}
	if a.kind == KindBool && !a.b || b.kind == KindBool && !b.b {
		return Bool(false), nil
	}
	if a.kind == KindNull || b.kind == KindNull {
		return Null(), nil
	}
	return Bool(true), nil
}

// Or is three-valued. true OR x is true; false OR x is x; null OR null is null.
func Or(a, b Value) (Value, error) {
	if err := expectLogic(a); err != nil {
		return Value{}, err
	}
	if err := expectLogic(b); err != nil {
		return Value{}, err
	}
	if a.kind == KindBool && a.b || b.kind == KindBool && b.b {
		return Bool(true), nil
	}
	if a.kind == KindNull || b.kind == KindNull {
		return Null(), nil
	}
	return Bool(false), nil
}

func expectLogic(v Value) error {
	if v.kind != KindNull && v.kind != KindBool {
		return fmt.Errorf("value: boolean operator expects bool or null, got %s", v.kind)
	}
	return nil
}

func comparableKinds(a, b Kind) bool {
	if a == b {
		return true
	}
	return (a == KindInt || a == KindFloat) && (b == KindInt || b == KindFloat)
}

func kindRank(k Kind) int {
	switch k {
	case KindNull:
		return 0
	case KindBool:
		return 1
	case KindInt, KindFloat:
		return 2
	case KindString:
		return 3
	case KindBytes:
		return 4
	case KindDate:
		return 5
	case KindDateTime:
		return 6
	case KindDuration:
		return 7
	case KindList:
		return 8
	case KindMap:
		return 9
	case KindNode:
		return 10
	case KindEdge:
		return 11
	case KindPath:
		return 12
	default:
		return 100 + int(k)
	}
}

func compareLists(a, b []Value) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a), len(b))
}

func compareMaps(a, b []kv) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := cmpString(a[i].key, b[i].key); c != 0 {
			return c
		}
		if c := Compare(a[i].val, b[i].val); c != 0 {
			return c
		}
	}
	return cmpInt(len(a), len(b))
}

func comparePaths(a, b Value) int {
	n := len(a.nodes)
	if len(b.nodes) < n {
		n = len(b.nodes)
	}
	for i := 0; i < n; i++ {
		if c := cmpUint64(a.nodes[i], b.nodes[i]); c != 0 {
			return c
		}
		aEdge := i < len(a.edges)
		bEdge := i < len(b.edges)
		if aEdge != bEdge {
			if aEdge {
				return 1
			}
			return -1
		}
		if aEdge {
			if c := cmpUint64(a.edges[i], b.edges[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a.nodes), len(b.nodes))
}

func cmpBool(a, b bool) int {
	if a == b {
		return 0
	}
	if !a {
		return -1
	}
	return 1
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpInt32(a, b int32) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpUint64(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return cmpInt(len(a), len(b))
}

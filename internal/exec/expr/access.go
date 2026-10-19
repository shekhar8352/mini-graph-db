package expr

import (
	"math"

	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func evalProp(n *ast.Property, env *Env) (value.Value, error) {
	x, err := eval(n.X, env)
	if err != nil {
		return value.Null(), err
	}
	return propOf(x, n.Name, env)
}

func propOf(x value.Value, name string, env *Env) (value.Value, error) {
	if x.Kind() == value.KindNull {
		return value.Null(), nil
	}
	switch x.Kind() {
	case value.KindMap:
		m, _ := x.MapValue()
		if v, ok := m[name]; ok {
			return v, nil
		}
		return value.Null(), nil
	case value.KindNode:
		id, _ := x.NodeID()
		n, err := needNode(env, id)
		if err != nil {
			return value.Null(), err
		}
		if v, ok := n.Props[name]; ok {
			return v, nil
		}
		return value.Null(), nil
	case value.KindEdge:
		id, _ := x.EdgeID()
		e, err := needEdge(env, id)
		if err != nil {
			return value.Null(), err
		}
		if v, ok := e.Props[name]; ok {
			return v, nil
		}
		return value.Null(), nil
	default:
		return value.Null(), inv("property on " + x.Kind().String())
	}
}

func evalIndex(n *ast.Index, env *Env) (value.Value, error) {
	x, err := eval(n.X, env)
	if err != nil {
		return value.Null(), err
	}
	i, err := eval(n.Index, env)
	if err != nil {
		return value.Null(), err
	}
	if x.Kind() == value.KindNull || i.Kind() == value.KindNull {
		return value.Null(), nil
	}
	switch x.Kind() {
	case value.KindList:
		el, _ := x.ListValue()
		at, ok := i.IntValue()
		if !ok {
			return value.Null(), inv("index expects an int")
		}
		return listAt(el, at), nil
	case value.KindMap, value.KindNode, value.KindEdge:
		key, ok := i.StringValue()
		if !ok {
			return value.Null(), inv("index expects a string")
		}
		return propOf(x, key, env)
	default:
		return value.Null(), inv("index on " + x.Kind().String())
	}
}

func listAt(el []value.Value, at int64) value.Value {
	n := int64(len(el))
	if at < 0 {
		at = n + at
	}
	if at < 0 || at >= n {
		return value.Null()
	}
	return el[at]
}

func evalSlice(n *ast.Slice, env *Env) (value.Value, error) {
	x, err := eval(n.X, env)
	if err != nil {
		return value.Null(), err
	}
	if x.Kind() == value.KindNull {
		return value.Null(), nil
	}
	el, ok := x.ListValue()
	if !ok {
		return value.Null(), inv("slice expects a list")
	}
	lo, err := eval(n.Low, env)
	if err != nil {
		return value.Null(), err
	}
	if lo.Kind() == value.KindNull {
		return value.Null(), nil
	}
	low, ok := lo.IntValue()
	if !ok {
		return value.Null(), inv("slice expects an int")
	}
	high := int64(len(el))
	if n.HasHigh {
		hi, err := eval(n.High, env)
		if err != nil {
			return value.Null(), err
		}
		if hi.Kind() == value.KindNull {
			return value.Null(), nil
		}
		high, ok = hi.IntValue()
		if !ok {
			return value.Null(), inv("slice expects an int")
		}
	}
	a := clampIdx(low, len(el))
	b := clampIdx(high, len(el))
	if a > b {
		return value.List(), nil
	}
	return value.List(el[a:b]...), nil
}

func clampIdx(i int64, n int) int {
	if i < 0 {
		i += int64(n)
	}
	if i < 0 {
		return 0
	}
	if i > int64(n) {
		return n
	}
	return int(i)
}

func isAgg(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max", "collect", "percentiledisc", "stdev":
		return true
	default:
		return false
	}
}

func fitID(id uint64) (value.Value, error) {
	if id > math.MaxInt64 {
		return value.Null(), inv("integer overflow")
	}
	return value.Int(int64(id)), nil
}

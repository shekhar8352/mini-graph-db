package expr

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func evalCall(n *ast.CallExpr, env *Env) (value.Value, error) {
	name := strings.ToLower(n.Name)
	if isAgg(name) {
		return value.Null(), gerr.Newf(gerr.Semantic, "%s is an aggregate", n.Name)
	}
	if n.Distinct {
		return value.Null(), gerr.New(gerr.Semantic, "DISTINCT is only legal on an aggregate")
	}
	if n.Star {
		return value.Null(), gerr.New(gerr.Semantic, "only count(*) takes *")
	}
	if name == "coalesce" {
		return fnCoalesce(n.Args, env)
	}
	args := make([]value.Value, len(n.Args))
	for i, a := range n.Args {
		v, err := eval(a, env)
		if err != nil {
			return value.Null(), err
		}
		args[i] = v
	}
	if !knownFunc(name) {
		return value.Null(), gerr.Newf(gerr.InvalidArgument, "unknown function %s", n.Name)
	}
	if err := checkArity(name, len(args)); err != nil {
		return value.Null(), err
	}
	if name != "typeof" {
		for _, a := range args {
			if a.Kind() == value.KindNull {
				return value.Null(), nil
			}
		}
	}
	return dispatch(name, args, env)
}

func knownFunc(name string) bool {
	switch name {
	case "toupper", "tolower", "trim", "split", "substring", "replace",
		"startswith", "endswith", "contains", "tostring", "size",
		"abs", "ceil", "floor", "round", "sqrt", "pow", "sign", "rand",
		"range", "head", "tail", "last", "reverse", "keys", "values",
		"date", "datetime", "duration", "now",
		"year", "month", "day", "quarter", "dayofweek",
		"hour", "minute", "second", "millisecond", "microsecond", "nanosecond", "offsetseconds",
		"months", "days", "nanoseconds", "hours", "minutes", "seconds",
		"toint", "tofloat", "tobool", "typeof", "bytes",
		"id", "labels", "type", "startnode", "endnode", "properties",
		"degree", "neighbors", "shortestpath", "allshortestpaths",
		"length", "nodes", "relationships", "coalesce":
		return true
	default:
		return false
	}
}

func checkArity(name string, n int) error {
	lo, hi := arity(name)
	if n < lo || (hi >= 0 && n > hi) {
		return gerr.Newf(gerr.Semantic, "%s has the wrong number of arguments", name)
	}
	return nil
}

func arity(name string) (int, int) {
	switch name {
	case "rand", "now":
		return 0, 0
	case "degree":
		return 1, 2
	case "substring", "range":
		return 2, 3
	case "split", "pow", "neighbors", "shortestpath", "allshortestpaths":
		return 2, 2
	case "replace":
		return 3, 3
	case "coalesce":
		return 1, -1
	default:
		return 1, 1
	}
}

func dispatch(name string, args []value.Value, env *Env) (value.Value, error) {
	switch name {
	case "toupper", "tolower", "trim", "split", "substring", "replace",
		"startswith", "endswith", "contains", "tostring", "size":
		return fnString(name, args)
	case "abs", "ceil", "floor", "round", "sqrt", "pow", "sign", "rand":
		return fnMath(name, args, env)
	case "range", "head", "tail", "last", "reverse", "keys", "values":
		return fnList(name, args, env)
	case "date", "datetime", "duration", "now",
		"year", "month", "day", "quarter", "dayofweek",
		"hour", "minute", "second", "millisecond", "microsecond", "nanosecond", "offsetseconds",
		"months", "days", "nanoseconds", "hours", "minutes", "seconds":
		return fnTime(name, args, env)
	case "toint", "tofloat", "tobool", "typeof", "bytes":
		return fnType(name, args)
	default:
		return fnGraph(name, args, env)
	}
}

func fnCoalesce(args []ast.Expr, env *Env) (value.Value, error) {
	if len(args) < 1 {
		return value.Null(), gerr.New(gerr.Semantic, "coalesce has the wrong number of arguments")
	}
	for _, a := range args {
		v, err := eval(a, env)
		if err != nil {
			return value.Null(), err
		}
		if v.Kind() != value.KindNull {
			return v, nil
		}
	}
	return value.Null(), nil
}

package sema

import "strings"

type sig struct {
	min  int
	max  int // negative means no upper bound
	ret  Type
	agg  bool
	args []Type
}

func init() {
	funcs = map[string]sig{}
	add := func(name string, s sig) { funcs[name] = s }
	one := func(name string, ret Type, arg Type) {
		add(name, sig{min: 1, max: 1, ret: ret, args: []Type{arg}})
	}
	for _, n := range []string{"toupper", "tolower", "trim"} {
		one(n, String, String)
	}
	one("tostring", String, Any)
	add("split", sig{min: 2, max: 2, ret: List, args: []Type{String, String}})
	add("substring", sig{min: 2, max: 3, ret: String, args: []Type{String, Int, Int}})
	add("replace", sig{min: 3, max: 3, ret: String, args: []Type{String, String, String}})
	for _, n := range []string{"startswith", "endswith", "contains"} {
		add(n, sig{min: 2, max: 2, ret: Bool, args: []Type{String, String}})
	}
	one("size", Int, Any)
	for _, n := range []string{"abs", "ceil", "floor", "round"} {
		one(n, Number, Number)
	}
	one("sqrt", Float, Number)
	add("pow", sig{min: 2, max: 2, ret: Float, args: []Type{Number, Number}})
	one("sign", Int, Number)
	add("rand", sig{min: 0, max: 0, ret: Float})
	add("range", sig{min: 2, max: 3, ret: List, args: []Type{Int, Int, Int}})
	one("head", Any, List)
	one("last", Any, List)
	one("tail", List, List)
	one("reverse", List, List)
	one("keys", List, Any)
	one("values", List, Map)
	one("date", Date, Any)
	one("datetime", DateTime, String)
	one("duration", Duration, String)
	add("now", sig{min: 0, max: 0, ret: DateTime})
	for _, n := range []string{
		"year", "month", "day", "quarter", "dayofweek",
		"hour", "minute", "second", "millisecond", "microsecond", "nanosecond", "offsetseconds",
		"months", "days", "nanoseconds", "hours", "minutes", "seconds",
	} {
		one(n, Int, Any)
	}
	one("toint", Int, Any)
	one("tofloat", Float, Any)
	one("tobool", Bool, Any)
	add("coalesce", sig{min: 1, max: -1, ret: Any})
	one("typeof", String, Any)
	one("id", Int, Entity)
	one("labels", List, Node)
	one("type", String, Edge)
	one("startnode", Node, Edge)
	one("endnode", Node, Edge)
	one("properties", Map, Any)
	add("degree", sig{min: 1, max: 2, ret: Int, args: []Type{Node, String}})
	add("neighbors", sig{min: 2, max: 2, ret: List, args: []Type{Node, Int}})
	add("shortestpath", sig{min: 2, max: 2, ret: Path, args: []Type{Node, Node}})
	add("allshortestpaths", sig{min: 2, max: 2, ret: List, args: []Type{Node, Node}})
	one("length", Int, Path)
	one("nodes", List, Path)
	one("relationships", List, Path)
	one("bytes", Bytes, String)
	add("count", sig{min: 0, max: 1, ret: Int, agg: true})
	for _, n := range []string{"min", "max"} {
		add(n, sig{min: 1, max: 1, ret: Any, agg: true})
	}
	add("sum", sig{min: 1, max: 1, ret: Number, agg: true})
	add("avg", sig{min: 1, max: 1, ret: Float, agg: true})
	add("collect", sig{min: 1, max: 1, ret: List, agg: true})
	add("percentiledisc", sig{min: 2, max: 2, ret: Any, agg: true, args: []Type{Any, Float}})
	add("stdev", sig{min: 1, max: 1, ret: Float, agg: true})
}

var funcs map[string]sig

func lookupFunc(name string) (sig, bool) {
	s, ok := funcs[strings.ToLower(name)]
	return s, ok
}

func isAggName(name string) bool {
	s, ok := lookupFunc(name)
	return ok && s.agg
}

var typeNames = map[string]Type{
	"NULL": Null, "BOOL": Bool, "INT": Int, "FLOAT": Float, "STRING": String,
	"BYTES": Bytes, "DATE": Date, "DATETIME": DateTime, "DURATION": Duration,
	"LIST": List, "MAP": Map, "NODE": Node, "EDGE": Edge, "PATH": Path,
}

func lookupTypeName(name string) (Type, bool) {
	t, ok := typeNames[strings.ToUpper(name)]
	return t, ok
}

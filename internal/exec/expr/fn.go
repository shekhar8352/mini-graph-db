package expr

import (
	"encoding/base64"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func fnString(name string, args []value.Value) (value.Value, error) {
	switch name {
	case "toupper", "tolower", "trim", "tostring":
		return fnString1(name, args[0])
	case "size":
		return fnSize(args[0])
	case "split":
		return fnSplit(args[0], args[1])
	case "substring":
		return fnSubstr(args)
	case "replace":
		return fnReplace(args[0], args[1], args[2])
	case "startswith":
		return strPred("STARTS WITH", args[0], args[1])
	case "endswith":
		return strPred("ENDS WITH", args[0], args[1])
	default:
		return strPred("CONTAINS", args[0], args[1])
	}
}

func fnString1(name string, v value.Value) (value.Value, error) {
	if name == "tostring" {
		if v.Kind() == value.KindNull {
			return value.Null(), nil
		}
		return value.String(value.Format(v)), nil
	}
	s, ok := v.StringValue()
	if !ok {
		return value.Null(), inv(name + " expects a string")
	}
	switch name {
	case "toupper":
		return value.String(strings.ToUpper(s)), nil
	case "tolower":
		return value.String(strings.ToLower(s)), nil
	default:
		return value.String(strings.TrimFunc(s, unicode.IsSpace)), nil
	}
}

func fnSize(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindString:
		s, _ := v.StringValue()
		return value.Int(int64(utf8.RuneCountInString(s))), nil
	case value.KindBytes:
		b, _ := v.BytesValue()
		return value.Int(int64(len(b))), nil
	case value.KindList:
		el, _ := v.ListValue()
		return value.Int(int64(len(el))), nil
	case value.KindMap:
		m, _ := v.MapValue()
		return value.Int(int64(len(m))), nil
	default:
		return value.Null(), inv("size expects a string, bytes, list, or map")
	}
}

func fnSplit(s, sep value.Value) (value.Value, error) {
	sv, ok1 := s.StringValue()
	pv, ok2 := sep.StringValue()
	if !ok1 || !ok2 {
		return value.Null(), inv("split expects strings")
	}
	if pv == "" {
		return value.Null(), inv("split separator is empty")
	}
	parts := strings.Split(sv, pv)
	out := make([]value.Value, len(parts))
	for i, p := range parts {
		out[i] = value.String(p)
	}
	return value.List(out...), nil
}

func fnSubstr(args []value.Value) (value.Value, error) {
	s, ok := args[0].StringValue()
	if !ok {
		return value.Null(), inv("substring expects a string")
	}
	start, ok := args[1].IntValue()
	if !ok || start < 0 {
		return value.Null(), inv("substring start is invalid")
	}
	runes := []rune(s)
	if start > int64(len(runes)) {
		start = int64(len(runes))
	}
	end := int64(len(runes))
	if len(args) == 3 {
		n, ok := args[2].IntValue()
		if !ok || n < 0 {
			return value.Null(), inv("substring length is invalid")
		}
		end = start + n
		if end < start || end > int64(len(runes)) {
			end = int64(len(runes))
		}
	}
	return value.String(string(runes[start:end])), nil
}

func fnReplace(s, search, repl value.Value) (value.Value, error) {
	sv, ok1 := s.StringValue()
	find, ok2 := search.StringValue()
	rp, ok3 := repl.StringValue()
	if !ok1 || !ok2 || !ok3 {
		return value.Null(), inv("replace expects strings")
	}
	if find == "" {
		return value.Null(), inv("replace search is empty")
	}
	return value.String(strings.ReplaceAll(sv, find, rp)), nil
}

func fnMath(name string, args []value.Value, env *Env) (value.Value, error) {
	switch name {
	case "rand":
		if env.Rand != nil {
			return value.Float(env.Rand()), nil
		}
		return value.Float(defaultRand()), nil
	case "pow":
		return pow(args[0], args[1])
	case "abs":
		return fnAbs(args[0])
	case "sign":
		return fnSign(args[0])
	case "sqrt":
		return fnSqrt(args[0])
	default:
		return fnRound(name, args[0])
	}
}

func fnAbs(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindInt:
		i, _ := v.IntValue()
		if i == math.MinInt64 {
			return value.Null(), inv("integer overflow")
		}
		if i < 0 {
			i = -i
		}
		return value.Int(i), nil
	case value.KindFloat:
		f, _ := v.FloatValue()
		return value.Float(math.Abs(f)), nil
	default:
		return value.Null(), inv("abs expects a number")
	}
}

func fnSign(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindInt:
		i, _ := v.IntValue()
		switch {
		case i < 0:
			return value.Int(-1), nil
		case i > 0:
			return value.Int(1), nil
		default:
			return value.Int(0), nil
		}
	case value.KindFloat:
		f, _ := v.FloatValue()
		switch {
		case math.IsNaN(f):
			return value.Null(), nil
		case f < 0:
			return value.Int(-1), nil
		case f > 0:
			return value.Int(1), nil
		default:
			return value.Int(0), nil
		}
	default:
		return value.Null(), inv("sign expects a number")
	}
}

func fnSqrt(v value.Value) (value.Value, error) {
	if !isNum(v) {
		return value.Null(), inv("sqrt expects a number")
	}
	f := asFloat(v)
	if f < 0 {
		return value.Null(), inv("sqrt of a negative number")
	}
	return value.Float(math.Sqrt(f)), nil
}

func fnRound(name string, v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindInt:
		return v, nil
	case value.KindFloat:
		f, _ := v.FloatValue()
		switch name {
		case "ceil":
			f = math.Ceil(f)
		case "floor":
			f = math.Floor(f)
		default:
			f = math.Round(f)
		}
		return value.Float(f), nil
	default:
		return value.Null(), inv(name + " expects a number")
	}
}

func fnList(name string, args []value.Value, env *Env) (value.Value, error) {
	switch name {
	case "head":
		return fnHead(args[0], true)
	case "last":
		return fnHead(args[0], false)
	case "tail":
		return fnTail(args[0])
	case "reverse":
		return fnReverse(args[0])
	case "keys":
		return fnKeys(args[0], env)
	case "values":
		return fnValues(args[0])
	default:
		return fnRange(args)
	}
}

func fnHead(v value.Value, first bool) (value.Value, error) {
	el, ok := v.ListValue()
	if !ok {
		return value.Null(), inv("list function expects a list")
	}
	if len(el) == 0 {
		return value.Null(), nil
	}
	if first {
		return el[0], nil
	}
	return el[len(el)-1], nil
}

func fnTail(v value.Value) (value.Value, error) {
	el, ok := v.ListValue()
	if !ok {
		return value.Null(), inv("tail expects a list")
	}
	if len(el) == 0 {
		return value.List(), nil
	}
	return value.List(el[1:]...), nil
}

func fnReverse(v value.Value) (value.Value, error) {
	el, ok := v.ListValue()
	if !ok {
		return value.Null(), inv("reverse expects a list")
	}
	out := make([]value.Value, len(el))
	for i := range el {
		out[i] = el[len(el)-1-i]
	}
	return value.List(out...), nil
}

func fnRange(args []value.Value) (value.Value, error) {
	start, ok1 := args[0].IntValue()
	end, ok2 := args[1].IntValue()
	if !ok1 || !ok2 {
		return value.Null(), inv("range expects integers")
	}
	step := int64(1)
	if len(args) == 3 {
		var ok bool
		step, ok = args[2].IntValue()
		if !ok {
			return value.Null(), inv("range expects integers")
		}
	}
	if step == 0 {
		return value.Null(), inv("range step is zero")
	}
	if (end-start < 0) != (step < 0) && start != end {
		return value.List(), nil
	}
	var out []value.Value
	for {
		out = append(out, value.Int(start))
		if start == end {
			break
		}
		next, ov := addInt(start, step)
		if ov {
			return value.Null(), inv("integer overflow")
		}
		if step > 0 && next > end || step < 0 && next < end {
			break
		}
		start = next
	}
	return value.List(out...), nil
}

func fnKeys(v value.Value, env *Env) (value.Value, error) {
	switch v.Kind() {
	case value.KindMap:
		return stringList(mapKeys(v)), nil
	case value.KindNode:
		id, _ := v.NodeID()
		n, err := needNode(env, id)
		if err != nil {
			return value.Null(), err
		}
		return stringList(propKeys(n.Props)), nil
	case value.KindEdge:
		id, _ := v.EdgeID()
		e, err := needEdge(env, id)
		if err != nil {
			return value.Null(), err
		}
		return stringList(propKeys(e.Props)), nil
	default:
		return value.Null(), inv("keys expects a map, node, or edge")
	}
}

func fnValues(v value.Value) (value.Value, error) {
	if v.Kind() != value.KindMap {
		return value.Null(), inv("values expects a map")
	}
	keys := mapKeys(v)
	m, _ := v.MapValue()
	out := make([]value.Value, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return value.List(out...), nil
}

func fnType(name string, args []value.Value) (value.Value, error) {
	v := args[0]
	switch name {
	case "typeof":
		return value.String(v.Kind().String()), nil
	case "bytes":
		s, ok := v.StringValue()
		if !ok {
			return value.Null(), inv("bytes expects a string")
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return value.Null(), inv("bytes is not standard base64")
		}
		return value.Bytes(raw), nil
	case "toint":
		return fnToInt(v)
	case "tofloat":
		return fnToFloat(v)
	default:
		return fnToBool(v)
	}
}

func fnToInt(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindInt:
		return v, nil
	case value.KindFloat:
		f, _ := v.FloatValue()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return value.Null(), inv("toInt expects a finite number")
		}
		if f >= math.MaxInt64 || f < math.MinInt64 {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(int64(f)), nil
	case value.KindString:
		s, _ := v.StringValue()
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return value.Null(), inv("toInt expects an integer literal")
		}
		return value.Int(i), nil
	default:
		return value.Null(), inv("toInt expects an int, float, or string")
	}
}

func fnToFloat(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindFloat:
		return v, nil
	case value.KindInt:
		i, _ := v.IntValue()
		return value.Float(float64(i)), nil
	case value.KindString:
		s, _ := v.StringValue()
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return value.Null(), inv("toFloat expects a float literal")
		}
		return value.Float(f), nil
	default:
		return value.Null(), inv("toFloat expects an int, float, or string")
	}
}

func fnToBool(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindBool:
		return v, nil
	case value.KindString:
		s, _ := v.StringValue()
		switch strings.ToLower(s) {
		case "true":
			return value.Bool(true), nil
		case "false":
			return value.Bool(false), nil
		default:
			return value.Null(), inv("toBool expects true or false")
		}
	default:
		return value.Null(), inv("toBool expects a bool or string")
	}
}

func stringList(keys []string) value.Value {
	out := make([]value.Value, len(keys))
	for i, k := range keys {
		out[i] = value.String(k)
	}
	return value.List(out...)
}

func mapKeys(v value.Value) []string {
	m, _ := v.MapValue()
	return propKeys(m)
}

func propKeys(m map[string]value.Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) { sort.Strings(s) }

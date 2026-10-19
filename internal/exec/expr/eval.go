package expr

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func eval(x ast.Expr, env *Env) (value.Value, error) {
	if x == nil {
		return value.Null(), gerr.New(gerr.InvalidArgument, "missing expression")
	}
	switch n := x.(type) {
	case *ast.Literal:
		return lit(n), nil
	case *ast.Ident:
		v, ok := env.lookup(n.Name)
		if !ok {
			return value.Null(), gerr.Newf(gerr.Semantic, "unbound variable %s", n.Name)
		}
		return v, nil
	case *ast.Param:
		v, ok := env.Params[n.Name]
		if !ok {
			return value.Null(), gerr.Newf(gerr.InvalidArgument, "missing parameter $%s", n.Name)
		}
		return v, nil
	case *ast.Unary:
		return evalUnary(n, env)
	case *ast.Binary:
		return evalBinary(n, env)
	case *ast.Pred:
		return evalPred(n, env)
	case *ast.Property:
		return evalProp(n, env)
	case *ast.Index:
		return evalIndex(n, env)
	case *ast.Slice:
		return evalSlice(n, env)
	case *ast.List:
		elems := make([]value.Value, len(n.Elems))
		for i, el := range n.Elems {
			v, err := eval(el, env)
			if err != nil {
				return value.Null(), err
			}
			elems[i] = v
		}
		return value.List(elems...), nil
	case *ast.MapLit:
		return evalMap(n, env)
	case *ast.Comp:
		return evalComp(n, env)
	case *ast.Quant:
		return evalQuant(n, env)
	case *ast.Reduce:
		return evalReduce(n, env)
	case *ast.Case:
		return evalCase(n, env)
	case *ast.CallExpr:
		return evalCall(n, env)
	default:
		return value.Null(), gerr.New(gerr.InvalidArgument, "unknown expression")
	}
}

func lit(n *ast.Literal) value.Value {
	switch n.Kind {
	case ast.LitNull:
		return value.Null()
	case ast.LitBool:
		return value.Bool(n.Bool)
	case ast.LitInt:
		return value.Int(n.Int)
	case ast.LitFloat:
		return value.Float(n.Float)
	case ast.LitString:
		return value.String(n.Str)
	case ast.LitNaN:
		return value.Float(nan())
	case ast.LitInf:
		return value.Float(inf(1))
	default:
		return value.Null()
	}
}

func evalUnary(n *ast.Unary, env *Env) (value.Value, error) {
	x, err := eval(n.X, env)
	if err != nil {
		return value.Null(), err
	}
	if n.Op == "NOT" {
		return logicNot(x)
	}
	if x.Kind() == value.KindNull {
		return value.Null(), nil
	}
	switch n.Op {
	case "+":
		if !isNum(x) {
			return value.Null(), inv("unary + expects a number")
		}
		return x, nil
	case "-":
		return negate(x)
	default:
		return value.Null(), inv("unknown unary operator")
	}
}

func evalBinary(n *ast.Binary, env *Env) (value.Value, error) {
	switch n.Op {
	case "AND":
		return evalAnd(n, env)
	case "OR":
		return evalOr(n, env)
	case "XOR":
		return evalXor(n, env)
	}
	l, err := eval(n.Left, env)
	if err != nil {
		return value.Null(), err
	}
	r, err := eval(n.Right, env)
	if err != nil {
		return value.Null(), err
	}
	switch n.Op {
	case "=", "<>", "<", "<=", ">", ">=":
		return compareOp(n.Op, l, r)
	case "IN":
		return evalIn(l, r)
	case "STARTS WITH", "ENDS WITH", "CONTAINS":
		return strPred(n.Op, l, r)
	default:
		return arith(n.Op, l, r)
	}
}

func evalAnd(n *ast.Binary, env *Env) (value.Value, error) {
	l, err := eval(n.Left, env)
	if err != nil {
		return value.Null(), err
	}
	if err := expectLogic(l); err != nil {
		return value.Null(), err
	}
	if l.Kind() == value.KindBool && !truth(l) {
		return value.Bool(false), nil
	}
	r, err := eval(n.Right, env)
	if err != nil {
		return value.Null(), err
	}
	if err := expectLogic(r); err != nil {
		return value.Null(), err
	}
	if r.Kind() == value.KindBool && !truth(r) {
		return value.Bool(false), nil
	}
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	return value.Bool(true), nil
}

func evalOr(n *ast.Binary, env *Env) (value.Value, error) {
	l, err := eval(n.Left, env)
	if err != nil {
		return value.Null(), err
	}
	if err := expectLogic(l); err != nil {
		return value.Null(), err
	}
	if l.Kind() == value.KindBool && truth(l) {
		return value.Bool(true), nil
	}
	r, err := eval(n.Right, env)
	if err != nil {
		return value.Null(), err
	}
	if err := expectLogic(r); err != nil {
		return value.Null(), err
	}
	if r.Kind() == value.KindBool && truth(r) {
		return value.Bool(true), nil
	}
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	return value.Bool(false), nil
}

func evalXor(n *ast.Binary, env *Env) (value.Value, error) {
	l, err := eval(n.Left, env)
	if err != nil {
		return value.Null(), err
	}
	r, err := eval(n.Right, env)
	if err != nil {
		return value.Null(), err
	}
	if err := expectLogic(l); err != nil {
		return value.Null(), err
	}
	if err := expectLogic(r); err != nil {
		return value.Null(), err
	}
	if l.Kind() == value.KindNull || r.Kind() == value.KindNull {
		return value.Null(), nil
	}
	return value.Bool(truth(l) != truth(r)), nil
}

func truth(v value.Value) bool {
	b, ok := v.BoolValue()
	return ok && b
}

func expectLogic(v value.Value) error {
	if v.Kind() != value.KindNull && v.Kind() != value.KindBool {
		return inv("boolean operator expects a boolean")
	}
	return nil
}

func logicNot(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindNull:
		return value.Null(), nil
	case value.KindBool:
		b, _ := v.BoolValue()
		return value.Bool(!b), nil
	default:
		return value.Null(), inv("NOT expects a boolean")
	}
}

func evalPred(n *ast.Pred, env *Env) (value.Value, error) {
	x, err := eval(n.X, env)
	if err != nil {
		return value.Null(), err
	}
	var ok bool
	if n.Type == "" {
		ok = x.Kind() == value.KindNull
	} else {
		ok = isType(x, n.Type)
	}
	if n.Not {
		ok = !ok
	}
	return value.Bool(ok), nil
}

func isType(v value.Value, name string) bool {
	want := strings.ToUpper(name)
	if v.Kind() == value.KindNull {
		return want == "NULL"
	}
	return strings.ToUpper(v.Kind().String()) == want
}

func evalMap(n *ast.MapLit, env *Env) (value.Value, error) {
	m := make(map[string]value.Value, len(n.Entries))
	for _, ent := range n.Entries {
		if _, ok := m[ent.Key]; ok {
			return value.Null(), gerr.Newf(gerr.Semantic, "duplicate map key %s", ent.Key)
		}
		v, err := eval(ent.Value, env)
		if err != nil {
			return value.Null(), err
		}
		m[ent.Key] = v
	}
	return value.Map(m), nil
}

func evalComp(n *ast.Comp, env *Env) (value.Value, error) {
	in, err := eval(n.In, env)
	if err != nil {
		return value.Null(), err
	}
	if in.Kind() == value.KindNull {
		return value.Null(), nil
	}
	elems, ok := in.ListValue()
	if !ok {
		return value.Null(), inv("IN expects a list")
	}
	var out []value.Value
	env.push()
	defer env.pop()
	for _, el := range elems {
		env.bind(n.Var, el)
		if n.Where != nil {
			w, err := eval(n.Where, env)
			if err != nil {
				return value.Null(), err
			}
			if !isTrue(w) {
				continue
			}
		}
		item := el
		if n.HasProj {
			item, err = eval(n.Proj, env)
			if err != nil {
				return value.Null(), err
			}
		}
		out = append(out, item)
	}
	return value.List(out...), nil
}

func evalQuant(n *ast.Quant, env *Env) (value.Value, error) {
	in, err := eval(n.In, env)
	if err != nil {
		return value.Null(), err
	}
	if in.Kind() == value.KindNull {
		return value.Null(), nil
	}
	elems, ok := in.ListValue()
	if !ok {
		return value.Null(), inv("IN expects a list")
	}
	kind := strings.ToUpper(n.Kind)
	trues := 0
	env.push()
	defer env.pop()
	for _, el := range elems {
		env.bind(n.Var, el)
		w, err := eval(n.Where, env)
		if err != nil {
			return value.Null(), err
		}
		if isTrue(w) {
			trues++
		}
	}
	switch kind {
	case "ANY":
		return value.Bool(trues > 0), nil
	case "ALL":
		return value.Bool(trues == len(elems)), nil
	case "NONE":
		return value.Bool(trues == 0), nil
	case "SINGLE":
		return value.Bool(trues == 1), nil
	default:
		return value.Null(), inv("unknown quantifier")
	}
}

func evalReduce(n *ast.Reduce, env *Env) (value.Value, error) {
	acc, err := eval(n.Init, env)
	if err != nil {
		return value.Null(), err
	}
	in, err := eval(n.In, env)
	if err != nil {
		return value.Null(), err
	}
	if in.Kind() == value.KindNull {
		return value.Null(), nil
	}
	elems, ok := in.ListValue()
	if !ok {
		return value.Null(), inv("IN expects a list")
	}
	env.push()
	defer env.pop()
	for _, el := range elems {
		env.bind(n.Acc, acc)
		env.bind(n.Var, el)
		acc, err = eval(n.Body, env)
		if err != nil {
			return value.Null(), err
		}
	}
	return acc, nil
}

func evalCase(n *ast.Case, env *Env) (value.Value, error) {
	var input value.Value
	if n.Input != nil {
		var err error
		input, err = eval(n.Input, env)
		if err != nil {
			return value.Null(), err
		}
	}
	for _, w := range n.Whens {
		cond, err := eval(w.Cond, env)
		if err != nil {
			return value.Null(), err
		}
		match := false
		if n.Input != nil {
			eq, err := compareOp("=", input, cond)
			if err != nil {
				return value.Null(), err
			}
			match = isTrue(eq)
		} else {
			match = isTrue(cond)
		}
		if !match {
			continue
		}
		return eval(w.Then, env)
	}
	if n.Else != nil {
		return eval(n.Else, env)
	}
	return value.Null(), nil
}

func isTrue(v value.Value) bool {
	b, ok := v.BoolValue()
	return ok && b
}

package sema

import (
	"strconv"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func (c *checker) expr(e ast.Expr) info {
	if c.err != nil || e == nil {
		return info{typ: Any}
	}
	switch n := e.(type) {
	case *ast.Literal:
		return info{typ: litType(n)}
	case *ast.Ident:
		return c.ident(n)
	case *ast.Param:
		return c.param(n)
	case *ast.Unary:
		return c.unary(n)
	case *ast.Binary:
		return c.binary(n)
	case *ast.Pred:
		return c.pred(n)
	case *ast.Property:
		return c.property(n)
	case *ast.Index:
		return c.index(n)
	case *ast.Slice:
		return c.slice(n)
	case *ast.List:
		elem := Any
		for i, el := range n.Elems {
			t := c.expr(el).typ
			if i == 0 {
				elem = t
				continue
			}
			if t != elem {
				elem = Any
			}
		}
		return info{typ: List, elem: elem}
	case *ast.MapLit:
		seen := map[string]bool{}
		for _, ent := range n.Entries {
			if seen[ent.Key] {
				c.fail(gerr.Semantic, "duplicate map key "+ent.Key, n)
				return info{typ: Map}
			}
			seen[ent.Key] = true
			c.expr(ent.Value)
		}
		return info{typ: Map}
	case *ast.Comp:
		return c.comp(n)
	case *ast.Quant:
		return c.quant(n)
	case *ast.Reduce:
		return c.reduce(n)
	case *ast.Case:
		c.expr(n.Input)
		for _, w := range n.Whens {
			c.expr(w.Cond)
			c.expr(w.Then)
		}
		c.expr(n.Else)
		return info{typ: Any}
	case *ast.CallExpr:
		return c.callExpr(n)
	default:
		return info{typ: Any}
	}
}

func litType(n *ast.Literal) Type {
	switch n.Kind {
	case ast.LitNull:
		return Null
	case ast.LitBool:
		return Bool
	case ast.LitInt:
		return Int
	case ast.LitFloat, ast.LitNaN, ast.LitInf:
		return Float
	case ast.LitString:
		return String
	default:
		return Any
	}
}

func (c *checker) ident(n *ast.Ident) info {
	if c.mode == modeNoRow {
		c.fail(gerr.Semantic, "row variable "+n.Name, n)
		return info{typ: Any}
	}
	inf, ok := c.lookup(n.Name)
	if !ok {
		c.fail(gerr.Semantic, "unbound variable "+n.Name, n)
		return info{typ: Any}
	}
	return inf
}

func (c *checker) param(n *ast.Param) info {
	if c.open {
		return info{typ: Any}
	}
	t, ok := c.params[n.Name]
	if !ok {
		c.fail(gerr.InvalidArgument, "missing parameter $"+n.Name, n)
		return info{typ: Any}
	}
	return info{typ: t}
}

func (c *checker) unary(n *ast.Unary) info {
	x := c.expr(n.X)
	if n.Op == "NOT" {
		if x.typ != Any && x.typ != Null && x.typ != Bool {
			c.fail(gerr.InvalidArgument, "NOT expects a boolean", n)
		}
		return info{typ: Bool}
	}
	if x.typ != Any && x.typ != Null && !numeric(x.typ) {
		c.fail(gerr.InvalidArgument, "unary "+n.Op+" expects a number", n)
		return info{typ: Any}
	}
	if x.typ == Float || x.typ == Number {
		return x
	}
	return info{typ: Int}
}

func (c *checker) binary(n *ast.Binary) info {
	l := c.expr(n.Left)
	r := c.expr(n.Right)
	if c.err != nil {
		return info{typ: Any}
	}
	switch n.Op {
	case "OR", "XOR", "AND", "=", "<>", "<", "<=", ">", ">=", "IN", "STARTS WITH", "ENDS WITH", "CONTAINS":
		c.predicate(n, l, r)
		return info{typ: Bool}
	case "+":
		return c.plus(n, l, r)
	case "-":
		return c.minus(n, l, r)
	case "*", "/":
		return c.mul(n, l, r)
	case "%":
		if !soft(l.typ, Int) || !soft(r.typ, Int) {
			c.fail(gerr.InvalidArgument, "% expects integers", n)
		}
		return info{typ: Int}
	case "^":
		if !soft(l.typ, Number) || !soft(r.typ, Number) {
			c.fail(gerr.InvalidArgument, "^ expects numbers", n)
		}
		return info{typ: Float}
	default:
		return info{typ: Any}
	}
}

func soft(got, want Type) bool {
	return got == Any || got == Null || compatible(got, want)
}

func (c *checker) predicate(n *ast.Binary, l, r info) {
	switch n.Op {
	case "IN":
		if r.typ != Any && r.typ != Null && r.typ != List {
			c.fail(gerr.InvalidArgument, "IN expects a list", n)
		}
	case "STARTS WITH", "ENDS WITH", "CONTAINS":
		if !soft(l.typ, String) || !soft(r.typ, String) {
			c.fail(gerr.InvalidArgument, n.Op+" expects strings", n)
		}
	}
}

func (c *checker) plus(n *ast.Binary, l, r info) info {
	if l.typ == Null || r.typ == Null {
		return info{typ: Null}
	}
	if l.typ == Any || r.typ == Any {
		return info{typ: Any}
	}
	if numeric(l.typ) && numeric(r.typ) {
		return info{typ: numType(l.typ, r.typ)}
	}
	if l.typ == String && r.typ == String {
		return info{typ: String}
	}
	if l.typ == List && r.typ == List {
		return info{typ: List}
	}
	if (l.typ == Date || l.typ == DateTime || l.typ == Duration) && r.typ == Duration {
		return l
	}
	c.fail(gerr.InvalidArgument, "invalid +", n)
	return info{typ: Any}
}

func (c *checker) minus(n *ast.Binary, l, r info) info {
	if l.typ == Null || r.typ == Null {
		return info{typ: Null}
	}
	if l.typ == Any || r.typ == Any {
		return info{typ: Any}
	}
	if numeric(l.typ) && numeric(r.typ) {
		return info{typ: numType(l.typ, r.typ)}
	}
	if l.typ == Duration && r.typ == Duration {
		return info{typ: Duration}
	}
	if (l.typ == Date && r.typ == Date) || (l.typ == DateTime && r.typ == DateTime) {
		return info{typ: Duration}
	}
	c.fail(gerr.InvalidArgument, "invalid -", n)
	return info{typ: Any}
}

func (c *checker) mul(n *ast.Binary, l, r info) info {
	if l.typ == Null || r.typ == Null {
		return info{typ: Null}
	}
	if l.typ == Any || r.typ == Any {
		return info{typ: Any}
	}
	if numeric(l.typ) && numeric(r.typ) {
		if n.Op == "/" && (l.typ == Float || r.typ == Float) {
			return info{typ: Float}
		}
		return info{typ: numType(l.typ, r.typ)}
	}
	if n.Op == "*" && ((l.typ == Duration && r.typ == Int) || (r.typ == Duration && l.typ == Int)) {
		return info{typ: Duration}
	}
	if n.Op == "/" && l.typ == Duration && r.typ == Int {
		return info{typ: Duration}
	}
	if n.Op == "*" && (l.typ == Duration && r.typ == Float || r.typ == Duration && l.typ == Float) {
		c.fail(gerr.InvalidArgument, "duration * float", n)
		return info{typ: Any}
	}
	c.fail(gerr.InvalidArgument, "invalid "+n.Op, n)
	return info{typ: Any}
}

func numType(a, b Type) Type {
	if a == Float || b == Float {
		return Float
	}
	if a == Number || b == Number {
		return Number
	}
	return Int
}

func (c *checker) pred(n *ast.Pred) info {
	c.expr(n.X)
	if n.Type != "" {
		if _, ok := lookupTypeName(n.Type); !ok {
			c.fail(gerr.Semantic, "unknown type "+n.Type, n)
		}
	}
	return info{typ: Bool}
}

func (c *checker) index(n *ast.Index) info {
	base := c.expr(n.X)
	idx := c.expr(n.Index)
	switch base.typ {
	case Any, Null:
		return info{typ: Any}
	case List:
		if !soft(idx.typ, Int) {
			c.fail(gerr.InvalidArgument, "index expects an int", n)
		}
		return info{typ: Any}
	case Map, Node, Edge, Entity:
		if !soft(idx.typ, String) {
			c.fail(gerr.InvalidArgument, "index expects a string", n)
		}
		return info{typ: Any}
	default:
		c.fail(gerr.InvalidArgument, "index on "+base.typ.String(), n)
		return info{typ: Any}
	}
}

func (c *checker) slice(n *ast.Slice) info {
	base := c.expr(n.X)
	low := c.expr(n.Low)
	high := c.expr(n.High)
	if n.Low != nil && !soft(low.typ, Int) {
		c.fail(gerr.InvalidArgument, "slice expects an int", n)
	}
	if n.High != nil && !soft(high.typ, Int) {
		c.fail(gerr.InvalidArgument, "slice expects an int", n)
	}
	switch base.typ {
	case Any, Null:
		return info{typ: Any}
	case List:
		return info{typ: List, elem: base.elem}
	default:
		c.fail(gerr.InvalidArgument, "slice expects a list", n)
		return info{typ: Any}
	}
}

func (c *checker) property(n *ast.Property) info {
	base := c.expr(n.X)
	if base.typ == List {
		c.fail(gerr.Semantic, "property on a list", n)
		return info{typ: Any}
	}
	switch base.typ {
	case Any, Null, Node, Edge, Map, Entity:
		return info{typ: Any}
	default:
		c.fail(gerr.InvalidArgument, "property on "+base.typ.String(), n)
		return info{typ: Any}
	}
}

func (c *checker) comp(n *ast.Comp) info {
	in := c.expr(n.In)
	c.listIn(in, n.In)
	elem := Any
	if in.typ == List {
		elem = in.elem
	}
	out := elem
	c.push()
	c.bindNew(n.Var, info{typ: elem}, n)
	c.expr(n.Where)
	if n.HasProj {
		out = c.expr(n.Proj).typ
	}
	c.pop()
	return info{typ: List, elem: out}
}

func (c *checker) quant(n *ast.Quant) info {
	in := c.expr(n.In)
	c.listIn(in, n.In)
	elem := Any
	if in.typ == List {
		elem = in.elem
	}
	c.push()
	c.bindNew(n.Var, info{typ: elem}, n)
	c.expr(n.Where)
	c.pop()
	return info{typ: Bool}
}

func (c *checker) reduce(n *ast.Reduce) info {
	init := c.expr(n.Init)
	in := c.expr(n.In)
	c.listIn(in, n.In)
	elem := Any
	if in.typ == List {
		elem = in.elem
	}
	c.push()
	c.bindNew(n.Acc, init, n)
	c.bindNew(n.Var, info{typ: elem}, n)
	body := c.expr(n.Body)
	c.pop()
	return body
}

func (c *checker) listIn(in info, at ast.Expr) {
	if in.typ != Any && in.typ != Null && in.typ != List {
		c.fail(gerr.InvalidArgument, "IN expects a list", at)
	}
}

func (c *checker) callExpr(n *ast.CallExpr) info {
	s, ok := lookupFunc(n.Name)
	if !ok {
		c.fail(gerr.InvalidArgument, "unknown function "+n.Name, n)
		return info{typ: Any}
	}
	if n.Distinct && !s.agg {
		c.fail(gerr.Semantic, "DISTINCT is only legal on an aggregate", n)
		return info{typ: s.ret}
	}
	if n.Star {
		if strings.ToLower(n.Name) != "count" || len(n.Args) != 0 {
			c.fail(gerr.Semantic, "only count(*) takes *", n)
			return info{typ: s.ret}
		}
	} else if !arity(s, len(n.Args)) {
		c.fail(gerr.Semantic, n.Name+" has the wrong number of arguments", n)
		return info{typ: s.ret}
	}
	if s.agg {
		if c.inAgg {
			c.fail(gerr.Semantic, "nested aggregate", n)
			return info{typ: s.ret}
		}
		if c.mode != modeAgg {
			c.fail(gerr.Semantic, "aggregate not allowed here", n)
			return info{typ: s.ret}
		}
	}
	prev := c.inAgg
	if s.agg {
		c.inAgg = true
	}
	var first info
	for i, a := range n.Args {
		got := c.expr(a)
		if i == 0 {
			first = got
		}
		if i < len(s.args) && !soft(got.typ, s.args[i]) {
			c.fail(gerr.InvalidArgument, n.Name+" argument "+strconv.Itoa(i+1), n)
		}
	}
	c.inAgg = prev
	ret := info{typ: s.ret}
	switch strings.ToLower(n.Name) {
	case "range":
		ret.elem = Int
	case "tail", "reverse":
		if first.typ == List {
			ret.elem = first.elem
		}
	}
	return ret
}

func arity(s sig, n int) bool {
	if n < s.min {
		return false
	}
	if s.max >= 0 && n > s.max {
		return false
	}
	return true
}

func (c *checker) hasAgg(e ast.Expr) bool {
	found := false
	ast.Walk(&aggFind{found: &found}, e)
	return found
}

type aggFind struct{ found *bool }

func (a *aggFind) Visit(n ast.Node) ast.Visitor {
	if n == nil || *a.found {
		return nil
	}
	if call, ok := n.(*ast.CallExpr); ok && isAggName(call.Name) {
		*a.found = true
	}
	return a
}

func (c *checker) rowOutsideAgg(e ast.Expr, keys map[string]bool, at ast.Node) {
	c.walkRow(e, false, keys, at)
}

func (c *checker) walkRow(e ast.Expr, inside bool, keys map[string]bool, at ast.Node) {
	if c.err != nil || e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.CallExpr:
		inn := inside || isAggName(n.Name)
		for _, a := range n.Args {
			c.walkRow(a, inn, keys, at)
		}
	case *ast.Ident:
		if !inside && !keys[n.Name] {
			c.fail(gerr.Semantic, "row variable "+n.Name+" is not a grouping key", n)
		}
	case *ast.Unary:
		c.walkRow(n.X, inside, keys, at)
	case *ast.Binary:
		c.walkRow(n.Left, inside, keys, at)
		c.walkRow(n.Right, inside, keys, at)
	case *ast.Pred:
		c.walkRow(n.X, inside, keys, at)
	case *ast.Property:
		c.walkRow(n.X, inside, keys, at)
	case *ast.Index:
		c.walkRow(n.X, inside, keys, at)
		c.walkRow(n.Index, inside, keys, at)
	case *ast.Slice:
		c.walkRow(n.X, inside, keys, at)
		c.walkRow(n.Low, inside, keys, at)
		c.walkRow(n.High, inside, keys, at)
	case *ast.List:
		for _, el := range n.Elems {
			c.walkRow(el, inside, keys, at)
		}
	case *ast.MapLit:
		for _, ent := range n.Entries {
			c.walkRow(ent.Value, inside, keys, at)
		}
	case *ast.Comp:
		c.walkRow(n.In, inside, keys, at)
		c.walkRow(n.Where, inside, keys, at)
		c.walkRow(n.Proj, inside, keys, at)
	case *ast.Quant:
		c.walkRow(n.In, inside, keys, at)
		c.walkRow(n.Where, inside, keys, at)
	case *ast.Reduce:
		c.walkRow(n.Init, inside, keys, at)
		c.walkRow(n.In, inside, keys, at)
		c.walkRow(n.Body, inside, keys, at)
	case *ast.Case:
		c.walkRow(n.Input, inside, keys, at)
		for _, w := range n.Whens {
			c.walkRow(w.Cond, inside, keys, at)
			c.walkRow(w.Then, inside, keys, at)
		}
		c.walkRow(n.Else, inside, keys, at)
	}
}

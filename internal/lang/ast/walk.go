package ast

import "reflect"

// Visitor visits each node. Visit returns the visitor used for the children.
// A nil return skips the children. Walk calls Visit(nil) after the children
// when the node was entered.
type Visitor interface {
	Visit(node Node) (w Visitor)
}

// Walk walks n in depth-first order.
func Walk(v Visitor, n Node) {
	if v == nil || n == nil {
		return
	}
	if v = v.Visit(n); v == nil {
		return
	}
	for _, c := range children(n) {
		Walk(v, c)
	}
	v.Visit(nil)
}

// Rewrite rewrites n bottom-up. f is called after the children and its result
// replaces the node. A nil result drops the node from a list. f must return an
// expression for an expression, a clause for a clause, and a statement for a
// statement. The root must stay non-nil.
func Rewrite(n Node, f func(Node) Node) Node {
	if n == nil {
		return nil
	}
	switch x := n.(type) {
	case *Script:
		x.Stmts = rewriteStmts(x.Stmts, f)
	case *Query:
		for i, p := range x.Parts {
			x.Parts[i] = rewriteAs(p, f)
		}
	case *SingleQuery:
		x.Clauses = rewriteClauses(x.Clauses, f)
	case *Explain:
		x.Query = rewriteAs(x.Query, f)
	case *Profile:
		x.Query = rewriteAs(x.Query, f)
	case *Pattern:
		for i, p := range x.Paths {
			x.Paths[i] = rewriteAs(p, f)
		}
	case *Path:
		for i, nd := range x.Nodes {
			x.Nodes[i] = rewriteAs(nd, f)
		}
		for i, r := range x.Rels {
			x.Rels[i] = rewriteAs(r, f)
		}
	case *NodePat:
		x.Props = rewriteAs(x.Props, f)
	case *RelPat:
		x.Props = rewriteAs(x.Props, f)
	case *Match:
		x.Pattern = rewriteAs(x.Pattern, f)
		x.Where = rewriteExpr(x.Where, f)
	case *Unwind:
		x.Expr = rewriteExpr(x.Expr, f)
	case *With:
		x.Body = rewriteAs(x.Body, f)
		x.Where = rewriteExpr(x.Where, f)
	case *Return:
		x.Body = rewriteAs(x.Body, f)
	case *ReturnBody:
		for i, it := range x.Items {
			x.Items[i] = rewriteAs(it, f)
		}
		for i, s := range x.Order {
			x.Order[i] = rewriteAs(s, f)
		}
		x.Skip = rewriteExpr(x.Skip, f)
		x.Limit = rewriteExpr(x.Limit, f)
	case *Projection:
		x.Expr = rewriteExpr(x.Expr, f)
	case *Sort:
		x.Expr = rewriteExpr(x.Expr, f)
	case *Call:
		x.Args = rewriteExprs(x.Args, f)
		for i, y := range x.Yield {
			x.Yield[i] = rewriteAs(y, f)
		}
		x.Where = rewriteExpr(x.Where, f)
	case *Create:
		x.Pattern = rewriteAs(x.Pattern, f)
	case *Merge:
		x.Path = rewriteAs(x.Path, f)
		x.OnCreate = rewriteAs(x.OnCreate, f)
		x.OnMatch = rewriteAs(x.OnMatch, f)
	case *Set:
		for i, it := range x.Items {
			x.Items[i] = rewriteAs(it, f)
		}
	case *SetItem:
		x.Value = rewriteExpr(x.Value, f)
	case *Remove:
		for i, it := range x.Items {
			x.Items[i] = rewriteAs(it, f)
		}
	case *Delete:
		x.Exprs = rewriteExprs(x.Exprs, f)
	case *Unary:
		x.X = rewriteExpr(x.X, f)
	case *Binary:
		x.Left = rewriteExpr(x.Left, f)
		x.Right = rewriteExpr(x.Right, f)
	case *Pred:
		x.X = rewriteExpr(x.X, f)
	case *Property:
		x.X = rewriteExpr(x.X, f)
	case *Index:
		x.X = rewriteExpr(x.X, f)
		x.Index = rewriteExpr(x.Index, f)
	case *Slice:
		x.X = rewriteExpr(x.X, f)
		x.Low = rewriteExpr(x.Low, f)
		x.High = rewriteExpr(x.High, f)
	case *List:
		x.Elems = rewriteExprs(x.Elems, f)
	case *MapLit:
		for i, e := range x.Entries {
			x.Entries[i] = rewriteAs(e, f)
		}
	case *MapEntry:
		x.Value = rewriteExpr(x.Value, f)
	case *Comp:
		x.In = rewriteExpr(x.In, f)
		x.Where = rewriteExpr(x.Where, f)
		x.Proj = rewriteExpr(x.Proj, f)
	case *Quant:
		x.In = rewriteExpr(x.In, f)
		x.Where = rewriteExpr(x.Where, f)
	case *Reduce:
		x.Init = rewriteExpr(x.Init, f)
		x.In = rewriteExpr(x.In, f)
		x.Body = rewriteExpr(x.Body, f)
	case *Case:
		x.Input = rewriteExpr(x.Input, f)
		for i, w := range x.Whens {
			x.Whens[i] = rewriteAs(w, f)
		}
		x.Else = rewriteExpr(x.Else, f)
	case *When:
		x.Cond = rewriteExpr(x.Cond, f)
		x.Then = rewriteExpr(x.Then, f)
	case *CallExpr:
		x.Args = rewriteExprs(x.Args, f)
	case *CreateIndex:
		x.For = rewriteAs(x.For, f)
		for i, p := range x.Props {
			x.Props[i] = rewriteAs(p, f)
		}
	case *CreateConstraint:
		x.For = rewriteAs(x.For, f)
	case *CreateToken:
		x.Expires = rewriteExpr(x.Expires, f)
	}
	if f == nil {
		return n
	}
	return f(n)
}

func children(n Node) []Node {
	switch x := n.(type) {
	case *Script:
		return stmts(x.Stmts)
	case *Query:
		return nodes(x.Parts)
	case *SingleQuery:
		return clauses(x.Clauses)
	case *Explain:
		return one(x.Query)
	case *Profile:
		return one(x.Query)
	case *Pattern:
		return nodes(x.Paths)
	case *Path:
		out := nodes(x.Nodes)
		out = append(out, nodes(x.Rels)...)
		return out
	case *NodePat:
		return one(x.Props)
	case *RelPat:
		return one(x.Props)
	case *Match:
		return join(one(x.Pattern), one(x.Where))
	case *Unwind:
		return one(x.Expr)
	case *With:
		return join(one(x.Body), one(x.Where))
	case *Return:
		return one(x.Body)
	case *ReturnBody:
		out := nodes(x.Items)
		out = append(out, nodes(x.Order)...)
		return join(out, one(x.Skip), one(x.Limit))
	case *Projection:
		return one(x.Expr)
	case *Sort:
		return one(x.Expr)
	case *Call:
		return join(exprs(x.Args), nodes(x.Yield), one(x.Where))
	case *Create:
		return one(x.Pattern)
	case *Merge:
		return join(one(x.Path), one(x.OnCreate), one(x.OnMatch))
	case *Set:
		return nodes(x.Items)
	case *SetItem:
		return one(x.Value)
	case *Remove:
		return nodes(x.Items)
	case *Delete:
		return exprs(x.Exprs)
	case *Unary:
		return one(x.X)
	case *Binary:
		return join(one(x.Left), one(x.Right))
	case *Pred:
		return one(x.X)
	case *Property:
		return one(x.X)
	case *Index:
		return join(one(x.X), one(x.Index))
	case *Slice:
		return join(one(x.X), one(x.Low), one(x.High))
	case *List:
		return exprs(x.Elems)
	case *MapLit:
		return nodes(x.Entries)
	case *MapEntry:
		return one(x.Value)
	case *Comp:
		return join(one(x.In), one(x.Where), one(x.Proj))
	case *Quant:
		return join(one(x.In), one(x.Where))
	case *Reduce:
		return join(one(x.Init), one(x.In), one(x.Body))
	case *Case:
		return join(one(x.Input), nodes(x.Whens), one(x.Else))
	case *When:
		return join(one(x.Cond), one(x.Then))
	case *CallExpr:
		return exprs(x.Args)
	case *CreateIndex:
		return join(one(x.For), nodes(x.Props))
	case *CreateConstraint:
		return one(x.For)
	case *CreateToken:
		return one(x.Expires)
	default:
		return nil
	}
}

func one(n any) []Node {
	if n == nil {
		return nil
	}
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return nil
	}
	node, ok := n.(Node)
	if !ok || node == nil {
		return nil
	}
	return []Node{node}
}

func join(parts ...[]Node) []Node {
	var out []Node
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func nodes[T Node](xs []T) []Node {
	if len(xs) == 0 {
		return nil
	}
	out := make([]Node, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func exprs(xs []Expr) []Node { return nodes(xs) }
func stmts(xs []Stmt) []Node { return nodes(xs) }
func clauses(xs []Clause) []Node {
	return nodes(xs)
}

func rewriteExpr(e Expr, f func(Node) Node) Expr {
	if e == nil {
		return nil
	}
	n := Rewrite(e, f)
	if n == nil {
		return nil
	}
	ex, ok := n.(Expr)
	if !ok {
		panic("ast.Rewrite replaced an expression with a non-expression")
	}
	return ex
}

func rewriteExprs(xs []Expr, f func(Node) Node) []Expr {
	var out []Expr
	for _, e := range xs {
		e = rewriteExpr(e, f)
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

func rewriteStmts(xs []Stmt, f func(Node) Node) []Stmt {
	var out []Stmt
	for _, s := range xs {
		n := Rewrite(s, f)
		if n == nil {
			continue
		}
		st, ok := n.(Stmt)
		if !ok {
			panic("ast.Rewrite replaced a statement with a non-statement")
		}
		out = append(out, st)
	}
	return out
}

func rewriteClauses(xs []Clause, f func(Node) Node) []Clause {
	var out []Clause
	for _, c := range xs {
		n := Rewrite(c, f)
		if n == nil {
			continue
		}
		cl, ok := n.(Clause)
		if !ok {
			panic("ast.Rewrite replaced a clause with a non-clause")
		}
		out = append(out, cl)
	}
	return out
}

func rewriteAs[T Node](n T, f func(Node) Node) T {
	var zero T
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return zero
	}
	out := Rewrite(n, f)
	if out == nil {
		return zero
	}
	got, ok := out.(T)
	if !ok {
		panic("ast.Rewrite replaced a node with a different type")
	}
	return got
}

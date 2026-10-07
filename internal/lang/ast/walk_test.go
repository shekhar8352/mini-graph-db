package ast

import "testing"

type order struct {
	seen []string
}

func (o *order) Visit(n Node) Visitor {
	if n == nil {
		o.seen = append(o.seen, "-")
		return nil
	}
	o.seen = append(o.seen, nodeKind(n))
	return o
}

func TestWalkOrder(t *testing.T) {
	tree := &Script{Stmts: []Stmt{
		&Query{Parts: []*SingleQuery{{
			Clauses: []Clause{
				&Return{Body: &ReturnBody{Items: []*Projection{{
					Expr: &Ident{Name: "n"},
				}}}},
			},
		}}},
	}}
	var o order
	Walk(&o, tree)
	want := []string{
		"Script", "Query", "SingleQuery", "Return", "ReturnBody", "Projection", "Ident",
		"-", "-", "-", "-", "-", "-", "-",
	}
	if len(o.seen) != len(want) {
		t.Fatalf("len %d got %v", len(o.seen), o.seen)
	}
	for i := range want {
		if o.seen[i] != want[i] {
			t.Fatalf("at %d got %v want %v", i, o.seen, want)
		}
	}
	var skipped order
	Walk(skipper{}, tree)
	if len(skipped.seen) != 0 {
		t.Fatalf("skipper recorded %v", skipped.seen)
	}
}

type skipper struct{}

func (skipper) Visit(Node) Visitor { return nil }

func TestRewriteIdent(t *testing.T) {
	e := &Binary{Op: "+", Left: &Ident{Name: "a"}, Right: &Ident{Name: "b"}}
	out := Rewrite(e, func(n Node) Node {
		id, ok := n.(*Ident)
		if ok && id.Name == "a" {
			return &Ident{Name: "z"}
		}
		return n
	})
	got := Format(out)
	if got != "z + b" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatParens(t *testing.T) {
	pow := &Binary{Op: "^", Left: &Literal{Kind: LitInt, Int: 2}, Right: &Literal{Kind: LitInt, Int: 2}}
	if got := Format(&Unary{Op: "-", X: pow}); got != "-2 ^ 2" {
		t.Fatalf("unary power: %q", got)
	}
	wrapped := &Binary{Op: "^", Left: &Unary{Op: "-", X: &Literal{Kind: LitInt, Int: 2}}, Right: &Literal{Kind: LitInt, Int: 2}}
	if got := Format(wrapped); got != "(-2) ^ 2" {
		t.Fatalf("power of unary: %q", got)
	}
}

func nodeKind(n Node) string {
	switch n.(type) {
	case *Script:
		return "Script"
	case *Query:
		return "Query"
	case *SingleQuery:
		return "SingleQuery"
	case *Return:
		return "Return"
	case *ReturnBody:
		return "ReturnBody"
	case *Projection:
		return "Projection"
	case *Ident:
		return "Ident"
	default:
		return "other"
	}
}

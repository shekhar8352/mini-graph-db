package ast

import (
	"math"
	"strconv"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
)

// Format prints n in canonical GQL-lite. Parse(Format(n)) matches n.
func Format(n Node) string {
	var b builder
	b.node(n)
	return b.String()
}

type builder struct {
	b strings.Builder
}

func (b *builder) String() string { return b.b.String() }

func (b *builder) raw(s string) { b.b.WriteString(s) }

func (b *builder) node(n Node) {
	if n == nil {
		return
	}
	switch x := n.(type) {
	case *Script:
		for i, s := range x.Stmts {
			if i > 0 {
				b.raw(";\n")
			}
			b.node(s)
		}
	case *Query:
		for i, p := range x.Parts {
			if i > 0 {
				b.raw(" UNION")
				if x.All[i-1] {
					b.raw(" ALL")
				}
				b.raw(" ")
			}
			b.node(p)
		}
	case *SingleQuery:
		for i, c := range x.Clauses {
			if i > 0 {
				b.raw(" ")
			}
			b.node(c)
		}
	case *Begin:
		b.raw("BEGIN")
		if x.ReadOnly {
			b.raw(" READ ONLY")
		}
	case *Commit:
		b.raw("COMMIT")
	case *Rollback:
		b.raw("ROLLBACK")
	case *Explain:
		b.raw("EXPLAIN ")
		b.node(x.Query)
	case *Profile:
		b.raw("PROFILE ")
		b.node(x.Query)
	case *Vacuum:
		b.raw("VACUUM")
	case *Analyze:
		b.raw("ANALYZE")
	case *Use:
		b.raw("USE ")
		b.name(x.Name, false)
	case *Terminate:
		b.raw("TERMINATE TRANSACTION ")
		b.raw(strconv.FormatInt(x.ID, 10))
	case *Show:
		b.raw("SHOW ")
		b.raw(string(x.Kind))
		if x.Kind == ShowPrivileges {
			b.raw(" FOR ")
			b.name(x.Name, false)
		}
	case *Match:
		if x.Optional {
			b.raw("OPTIONAL ")
		}
		b.raw("MATCH ")
		b.node(x.Pattern)
		b.where(x.Where)
	case *Unwind:
		b.raw("UNWIND ")
		b.expr(x.Expr, 0, false)
		b.raw(" AS ")
		b.name(x.Name, true)
	case *With:
		b.raw("WITH ")
		b.body(x.Body)
		b.where(x.Where)
	case *Return:
		b.raw("RETURN ")
		b.body(x.Body)
	case *Create:
		b.raw("CREATE ")
		b.node(x.Pattern)
	case *Merge:
		b.raw("MERGE ")
		b.node(x.Path)
		if x.OnCreate != nil {
			b.raw(" ON CREATE ")
			b.node(x.OnCreate)
		}
		if x.OnMatch != nil {
			b.raw(" ON MATCH ")
			b.node(x.OnMatch)
		}
	case *Set:
		b.raw("SET ")
		for i, it := range x.Items {
			if i > 0 {
				b.raw(", ")
			}
			b.setItem(it)
		}
	case *Remove:
		b.raw("REMOVE ")
		for i, it := range x.Items {
			if i > 0 {
				b.raw(", ")
			}
			b.name(it.Name, false)
			if it.Prop != "" {
				b.raw(".")
				b.name(it.Prop, true)
			} else {
				b.labels(it.Labels)
			}
		}
	case *Delete:
		if x.Detach {
			b.raw("DETACH ")
		}
		b.raw("DELETE ")
		b.exprList(x.Exprs)
	case *Call:
		b.raw("CALL ")
		for i, part := range x.Proc {
			if i > 0 {
				b.raw(".")
			}
			b.name(part, i > 0)
		}
		b.raw("(")
		b.exprList(x.Args)
		b.raw(")")
		if len(x.Yield) > 0 {
			b.raw(" YIELD ")
			for i, y := range x.Yield {
				if i > 0 {
					b.raw(", ")
				}
				b.name(y.Name, true)
				if y.As != "" {
					b.raw(" AS ")
					b.name(y.As, true)
				}
			}
		}
		b.where(x.Where)
	case *Pattern:
		for i, p := range x.Paths {
			if i > 0 {
				b.raw(", ")
			}
			b.node(p)
		}
	case *Path:
		if x.Var != "" {
			b.name(x.Var, false)
			b.raw(" = ")
		}
		switch x.Fn {
		case PathShortest:
			b.raw("shortestPath(")
		case PathAllShortest:
			b.raw("allShortestPaths(")
		}
		for i, n := range x.Nodes {
			if i > 0 {
				b.rel(x.Rels[i-1])
			}
			b.vertex(n)
		}
		if x.Fn != PathPlain {
			b.raw(")")
		}
	case *CreateIndex:
		b.raw("CREATE INDEX ")
		b.name(x.Name, false)
		b.raw(" FOR ")
		b.indexFor(x.For)
		b.raw(" ON (")
		for i, p := range x.Props {
			if i > 0 {
				b.raw(", ")
			}
			b.name(p.Var, false)
			b.raw(".")
			b.name(p.Prop, true)
		}
		b.raw(")")
	case *DropIndex:
		b.raw("DROP INDEX ")
		b.name(x.Name, false)
	case *CreateConstraint:
		b.raw("CREATE CONSTRAINT ")
		b.name(x.Name, false)
		b.raw(" FOR ")
		b.indexFor(x.For)
		b.raw(" REQUIRE ")
		b.name(x.Var, false)
		b.raw(".")
		b.name(x.Prop, true)
		b.raw(" IS ")
		switch x.Kind {
		case ConstraintExists:
			b.raw("NOT NULL")
		case ConstraintType:
			b.raw(":: ")
			b.name(x.Type, true)
		default:
			b.raw("UNIQUE")
		}
	case *DropConstraint:
		b.raw("DROP CONSTRAINT ")
		b.name(x.Name, false)
	case *CreateDatabase:
		b.raw("CREATE DATABASE ")
		b.name(x.Name, false)
	case *DropDatabase:
		b.raw("DROP DATABASE ")
		b.name(x.Name, false)
		if x.Confirm {
			b.raw(" CONFIRM")
		}
	case *CreateUser:
		b.user("CREATE", x.Name, x.Password, x.ChangeRequired)
	case *AlterUser:
		b.user("ALTER", x.Name, x.Password, x.ChangeRequired)
	case *DropUser:
		b.raw("DROP USER ")
		b.name(x.Name, false)
	case *CreateRole:
		b.raw("CREATE ROLE ")
		b.name(x.Name, false)
	case *DropRole:
		b.raw("DROP ROLE ")
		b.name(x.Name, false)
	case *GrantRole:
		b.raw("GRANT ROLE ")
		b.name(x.Role, false)
		b.raw(" TO ")
		b.name(x.To, false)
	case *RevokeRole:
		b.raw("REVOKE ROLE ")
		b.name(x.Role, false)
		b.raw(" FROM ")
		b.name(x.From, false)
	case *GrantPriv:
		b.priv("GRANT", x.Priv, x.Database, "TO", x.To)
	case *RevokePriv:
		b.priv("REVOKE", x.Priv, x.Database, "FROM", x.From)
	case *DenyPriv:
		b.priv("DENY", x.Priv, x.Database, "TO", x.To)
	case *CreateToken:
		b.raw("CREATE TOKEN FOR USER ")
		b.name(x.User, false)
		b.raw(" EXPIRES IN ")
		b.expr(x.Expires, 0, false)
	case *RevokeToken:
		b.raw("REVOKE TOKEN ")
		b.raw(strconv.FormatInt(x.ID, 10))
	default:
		b.expr(n.(Expr), 0, false)
	}
}

func (b *builder) where(e Expr) {
	if e == nil {
		return
	}
	b.raw(" WHERE ")
	b.expr(e, 0, false)
}

func (b *builder) body(x *ReturnBody) {
	if x == nil {
		return
	}
	if x.Distinct {
		b.raw("DISTINCT ")
	}
	if x.Star {
		b.raw("*")
	} else {
		for i, it := range x.Items {
			if i > 0 {
				b.raw(", ")
			}
			b.expr(it.Expr, 0, false)
			if it.As != "" {
				b.raw(" AS ")
				b.name(it.As, true)
			}
		}
	}
	if len(x.Order) > 0 {
		b.raw(" ORDER BY ")
		for i, s := range x.Order {
			if i > 0 {
				b.raw(", ")
			}
			b.expr(s.Expr, 0, false)
			if s.Desc {
				b.raw(" DESC")
			}
			switch s.Nulls {
			case NullsFirst:
				b.raw(" NULLS FIRST")
			case NullsLast:
				b.raw(" NULLS LAST")
			}
		}
	}
	if x.Skip != nil {
		b.raw(" SKIP ")
		b.expr(x.Skip, 0, false)
	}
	if x.Limit != nil {
		b.raw(" LIMIT ")
		b.expr(x.Limit, 0, false)
	}
}

func (b *builder) setItem(it *SetItem) {
	b.name(it.Name, false)
	switch it.Op {
	case SetProp:
		b.raw(".")
		b.name(it.Prop, true)
		b.raw(" = ")
		b.expr(it.Value, 0, false)
	case SetReplace:
		b.raw(" = ")
		b.expr(it.Value, 0, false)
	case SetPlus:
		b.raw(" += ")
		b.expr(it.Value, 0, false)
	default:
		b.labels(it.Labels)
	}
}

func (b *builder) user(verb, name, password string, change bool) {
	b.raw(verb)
	b.raw(" USER ")
	b.name(name, false)
	b.raw(" SET PASSWORD ")
	b.raw(strconv.Quote(password))
	if change {
		b.raw(" CHANGE REQUIRED")
	}
}

func (b *builder) priv(verb string, p Priv, db, dir, who string) {
	b.raw(verb)
	b.raw(" ")
	b.raw(string(p))
	b.raw(" ON DATABASE ")
	b.name(db, false)
	b.raw(" ")
	b.raw(dir)
	b.raw(" ")
	b.name(who, false)
}

func (b *builder) indexFor(f *IndexFor) {
	if f == nil {
		return
	}
	if f.Edge {
		b.raw("()-[")
		b.name(f.Var, false)
		b.raw(":")
		b.name(f.On, true)
		b.raw("]-()")
		return
	}
	b.raw("(")
	b.name(f.Var, false)
	b.raw(":")
	b.name(f.On, true)
	b.raw(")")
}

func (b *builder) vertex(n *NodePat) {
	b.raw("(")
	if n != nil && n.Name != "" {
		b.name(n.Name, false)
	}
	if n != nil {
		b.labels(n.Labels)
		if n.Props != nil {
			if n.Name != "" || len(n.Labels) > 0 {
				b.raw(" ")
			}
			b.mapLit(n.Props)
		}
	}
	b.raw(")")
}

func (b *builder) labels(groups [][]string) {
	for _, g := range groups {
		b.raw(":")
		for i, name := range g {
			if i > 0 {
				b.raw("|")
			}
			b.name(name, true)
		}
	}
}

func (b *builder) rel(r *RelPat) {
	empty := r.Name == "" && len(r.Types) == 0 && !r.VarLen && r.Props == nil
	switch r.Dir {
	case DirIn:
		if empty {
			b.raw("<--")
			return
		}
		b.raw("<-")
		b.relBody(r)
		b.raw("-")
	case DirEither:
		if empty {
			b.raw("- -")
			return
		}
		b.raw("-")
		b.relBody(r)
		b.raw("-")
	default:
		if empty {
			b.raw("-->")
			return
		}
		b.raw("-")
		b.relBody(r)
		b.raw("->")
	}
}

func (b *builder) relBody(r *RelPat) {
	b.raw("[")
	if r.Name != "" {
		b.name(r.Name, false)
	}
	if len(r.Types) > 0 {
		b.raw(":")
		for i, t := range r.Types {
			if i > 0 {
				b.raw("|")
			}
			b.name(t, true)
		}
	}
	if r.VarLen {
		b.raw("*")
		switch {
		case r.Min == r.Max:
			b.raw(strconv.FormatInt(r.Min, 10))
		case r.Min == 1 && r.Max < 0:
		case r.Min == 1:
			b.raw("..")
			b.raw(strconv.FormatInt(r.Max, 10))
		case r.Max < 0:
			b.raw(strconv.FormatInt(r.Min, 10))
			b.raw("..")
		default:
			b.raw(strconv.FormatInt(r.Min, 10))
			b.raw("..")
			b.raw(strconv.FormatInt(r.Max, 10))
		}
	}
	if r.Props != nil {
		if r.Name != "" || len(r.Types) > 0 || r.VarLen {
			b.raw(" ")
		}
		b.mapLit(r.Props)
	}
	b.raw("]")
}

func (b *builder) exprList(xs []Expr) {
	for i, e := range xs {
		if i > 0 {
			b.raw(", ")
		}
		b.expr(e, 0, false)
	}
}

func (b *builder) expr(e Expr, parent int, right bool) {
	if e == nil {
		return
	}
	if paren(e, parent, right) {
		b.raw("(")
		b.writeExpr(e)
		b.raw(")")
		return
	}
	b.writeExpr(e)
}

func (b *builder) writeExpr(e Expr) {
	switch x := e.(type) {
	case *Literal:
		b.literal(x)
	case *Ident:
		b.name(x.Name, false)
	case *Param:
		b.raw("$")
		if lexer.IsKeyword(x.Name) || !plainIdent(x.Name) {
			b.name(x.Name, false)
		} else {
			b.raw(x.Name)
		}
	case *Unary:
		b.raw(x.Op)
		if x.Op == "NOT" {
			b.raw(" ")
		}
		b.expr(x.X, prec(x), true)
	case *Binary:
		p := prec(x)
		b.expr(x.Left, p, false)
		b.raw(" ")
		b.raw(x.Op)
		b.raw(" ")
		b.expr(x.Right, p, true)
	case *Pred:
		b.expr(x.X, prec(x), false)
		b.raw(" IS ")
		if x.Not {
			b.raw("NOT ")
		}
		if x.Type != "" {
			b.raw(":: ")
			b.name(x.Type, true)
		} else {
			b.raw("NULL")
		}
	case *Property:
		b.expr(x.X, prec(x), false)
		b.raw(".")
		b.name(x.Name, true)
	case *Index:
		b.expr(x.X, prec(x), false)
		b.raw("[")
		b.expr(x.Index, 0, false)
		b.raw("]")
	case *Slice:
		b.expr(x.X, prec(x), false)
		b.raw("[")
		b.expr(x.Low, 0, false)
		b.raw("..")
		if x.HasHigh {
			b.expr(x.High, 0, false)
		}
		b.raw("]")
	case *List:
		b.raw("[")
		b.exprList(x.Elems)
		b.raw("]")
	case *MapLit:
		b.mapLit(x)
	case *Comp:
		b.raw("[")
		b.name(x.Var, false)
		b.raw(" IN ")
		b.expr(x.In, 0, false)
		if x.Where != nil {
			b.raw(" WHERE ")
			b.expr(x.Where, 0, false)
		}
		if x.HasProj {
			b.raw(" | ")
			b.expr(x.Proj, 0, false)
		}
		b.raw("]")
	case *Quant:
		b.raw(x.Kind)
		b.raw("(")
		b.name(x.Var, false)
		b.raw(" IN ")
		b.expr(x.In, 0, false)
		b.raw(" WHERE ")
		b.expr(x.Where, 0, false)
		b.raw(")")
	case *Reduce:
		b.raw("REDUCE(")
		b.name(x.Acc, false)
		b.raw(" = ")
		b.expr(x.Init, 0, false)
		b.raw(", ")
		b.name(x.Var, false)
		b.raw(" IN ")
		b.expr(x.In, 0, false)
		b.raw(" | ")
		b.expr(x.Body, 0, false)
		b.raw(")")
	case *Case:
		b.raw("CASE")
		if x.Input != nil {
			b.raw(" ")
			b.expr(x.Input, 0, false)
		}
		for _, w := range x.Whens {
			b.raw(" WHEN ")
			b.expr(w.Cond, 0, false)
			b.raw(" THEN ")
			b.expr(w.Then, 0, false)
		}
		if x.Else != nil {
			b.raw(" ELSE ")
			b.expr(x.Else, 0, false)
		}
		b.raw(" END")
	case *CallExpr:
		b.name(x.Name, true)
		b.raw("(")
		if x.Distinct {
			b.raw("DISTINCT ")
		}
		if x.Star {
			b.raw("*")
		} else {
			b.exprList(x.Args)
		}
		b.raw(")")
	}
}

func (b *builder) mapLit(m *MapLit) {
	b.raw("{")
	for i, e := range m.Entries {
		if i > 0 {
			b.raw(", ")
		}
		if e.Quoted {
			b.raw(strconv.Quote(e.Key))
		} else {
			b.name(e.Key, true)
		}
		b.raw(": ")
		b.expr(e.Value, 0, false)
	}
	b.raw("}")
}

func (b *builder) literal(x *Literal) {
	switch x.Kind {
	case LitNull:
		b.raw("NULL")
	case LitBool:
		if x.Bool {
			b.raw("TRUE")
		} else {
			b.raw("FALSE")
		}
	case LitInt:
		b.raw(strconv.FormatInt(x.Int, 10))
	case LitFloat:
		b.raw(formatFloat(x.Float))
	case LitString:
		b.raw(strconv.Quote(x.Str))
	case LitNaN:
		b.raw("NAN")
	case LitInf:
		b.raw("INF")
	}
}

func (b *builder) name(s string, keywordOK bool) {
	if plainIdent(s) && (keywordOK || !lexer.IsKeyword(s)) {
		b.raw(s)
		return
	}
	b.raw("`")
	b.raw(strings.ReplaceAll(s, "`", "``"))
	b.raw("`")
}

func plainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "1e309"
	}
	if math.IsInf(f, -1) {
		return "-1e309"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func prec(e Expr) int {
	switch x := e.(type) {
	case *Binary:
		switch x.Op {
		case "OR":
			return 1
		case "XOR":
			return 2
		case "AND":
			return 3
		case "+", "-":
			return 6
		case "*", "/", "%":
			return 7
		case "^":
			return 9
		default:
			return 5
		}
	case *Pred:
		return 5
	case *Unary:
		if x.Op == "NOT" {
			return 4
		}
		return 8
	case *Property, *Index, *Slice, *CallExpr:
		return 10
	default:
		return 11
	}
}

func rightAssoc(e Expr) bool {
	b, ok := e.(*Binary)
	return ok && b.Op == "^"
}

func paren(e Expr, parent int, right bool) bool {
	if parent == 0 {
		return false
	}
	p := prec(e)
	if p < parent {
		return true
	}
	if p > parent {
		return false
	}
	if parent == 5 {
		return true
	}
	if rightAssoc(e) {
		return !right
	}
	return right
}

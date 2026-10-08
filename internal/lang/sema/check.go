// Package sema checks a GQL-lite script against the scope, clause, and type rules.
// It does not run the query. Runtime failures stay with execution.
package sema

import (
	"regexp"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
)

// Check parses src and applies the semantic rules.
// A parameter is allowed and has type Any.
func Check(src string) error {
	return checkSrc(src, nil, true)
}

// CheckParams is Check, except every parameter the script reads must be a key of params.
// A missing name is InvalidArgument. The mapped type is checked where both sides are known.
func CheckParams(src string, params map[string]Type) error {
	if params == nil {
		params = map[string]Type{}
	}
	return checkSrc(src, params, false)
}

func checkSrc(src string, params map[string]Type, open bool) error {
	script, pos, err := parser.ParseSpan(src)
	if err != nil {
		return err
	}
	c := &checker{
		pos:    pos,
		params: params,
		open:   open,
		layers: []map[string]info{{}},
		order:  [][]string{nil},
	}
	c.script(script)
	return c.err
}

type info struct {
	typ  Type
	elem Type
}

type mode int

const (
	modeValue mode = iota
	modeAgg
	modeNoRow
)

type checker struct {
	pos    map[ast.Node]parser.Pos
	params map[string]Type
	open   bool
	err    error
	layers []map[string]info
	order  [][]string
	mode   mode
	inAgg  bool
}

func (c *checker) script(s *ast.Script) {
	if s == nil {
		return
	}
	for _, st := range s.Stmts {
		c.stmt(st)
		if c.err != nil {
			return
		}
	}
}

func (c *checker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.Query:
		c.query(s)
	case *ast.Explain:
		c.query(s.Query)
	case *ast.Profile:
		c.query(s.Query)
	case *ast.Begin, *ast.Commit, *ast.Rollback, *ast.Vacuum, *ast.Analyze,
		*ast.Show, *ast.Terminate, *ast.CreateUser, *ast.AlterUser, *ast.DropUser,
		*ast.CreateRole, *ast.DropRole, *ast.GrantRole, *ast.RevokeRole, *ast.RevokeToken:
		c.namesInStmt(s)
	case *ast.GrantPriv:
		c.database(s.Database, false, s)
	case *ast.RevokePriv:
		c.database(s.Database, false, s)
	case *ast.DenyPriv:
		c.database(s.Database, false, s)
	case *ast.Use:
		c.database(s.Name, false, s)
	case *ast.CreateDatabase:
		c.database(s.Name, true, s)
	case *ast.DropDatabase:
		if !s.Confirm {
			c.fail(gerr.Semantic, "DROP DATABASE requires CONFIRM", s)
			return
		}
		c.database(s.Name, true, s)
	case *ast.CreateIndex:
		c.createIndex(s)
	case *ast.DropIndex, *ast.DropConstraint:
	case *ast.CreateConstraint:
		c.createConstraint(s)
	case *ast.CreateToken:
		c.expr(s.Expires)
	default:
		c.namesInStmt(s)
	}
}

func (c *checker) query(q *ast.Query) {
	if q == nil {
		return
	}
	var cols []string
	for i, part := range q.Parts {
		c.resetScope()
		got := c.single(part)
		if c.err != nil {
			return
		}
		if i == 0 {
			cols = got
			continue
		}
		if len(got) != len(cols) || !sameNames(got, cols) {
			c.fail(gerr.Semantic, "UNION columns do not match", part)
			return
		}
	}
}

func sameNames(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *checker) resetScope() {
	c.layers = []map[string]info{{}}
	c.order = [][]string{nil}
}

func (c *checker) single(q *ast.SingleQuery) []string {
	if q == nil || len(q.Clauses) == 0 {
		c.fail(gerr.Semantic, "query has no clause", q)
		return nil
	}
	write := false
	var cols []string
	returned := false
	for i, cl := range q.Clauses {
		last := i == len(q.Clauses)-1
		switch n := cl.(type) {
		case *ast.Match:
			c.match(n)
		case *ast.Unwind:
			c.unwind(n)
		case *ast.With:
			if last {
				c.fail(gerr.Semantic, "WITH is the last clause", n)
			}
			c.with(n)
		case *ast.Return:
			if !last {
				c.fail(gerr.Semantic, "RETURN is not the last clause", n)
			}
			returned = true
			cols = c.ret(n)
		case *ast.Create:
			write = true
			c.pattern(n.Pattern, patCreate)
		case *ast.Merge:
			write = true
			c.merge(n)
		case *ast.Set:
			write = true
			c.set(n)
		case *ast.Remove:
			write = true
			c.remove(n)
		case *ast.Delete:
			write = true
			c.del(n)
		case *ast.Call:
			if !last && len(n.Yield) == 0 {
				c.fail(gerr.Semantic, "CALL needs YIELD", n)
			}
			c.call(n)
			if last && len(q.Clauses) == 1 {
				cols = c.callCols(n)
			}
		}
		if c.err != nil {
			return nil
		}
	}
	onlyCall := len(q.Clauses) == 1
	if _, ok := q.Clauses[0].(*ast.Call); ok && onlyCall {
		return cols
	}
	if !write && !returned {
		c.fail(gerr.Semantic, "read query has no RETURN", q)
	}
	return cols
}

type patKind int

const (
	patMatch patKind = iota
	patCreate
	patMerge
)

func (c *checker) match(m *ast.Match) {
	c.pattern(m.Pattern, patMatch)
	c.where(m.Where)
}

func (c *checker) pattern(p *ast.Pattern, k patKind) {
	if p == nil {
		return
	}
	if k == patMerge && len(p.Paths) != 1 {
		c.fail(gerr.Semantic, "MERGE pattern is not a single chain", p)
		return
	}
	for _, path := range p.Paths {
		c.path(path, k)
	}
}

func (c *checker) merge(m *ast.Merge) {
	if m.Path == nil {
		return
	}
	if m.Path.Fn != ast.PathPlain {
		c.fail(gerr.Semantic, "MERGE pattern is not a single chain", m)
		return
	}
	for _, r := range m.Path.Rels {
		if r != nil && r.VarLen {
			c.fail(gerr.Semantic, "variable-length relationship in MERGE", r)
			return
		}
	}
	c.path(m.Path, patMerge)
	if m.OnCreate != nil {
		c.set(m.OnCreate)
	}
	if m.OnMatch != nil {
		c.set(m.OnMatch)
	}
}

func (c *checker) path(p *ast.Path, k patKind) {
	if p == nil {
		return
	}
	if p.Fn != ast.PathPlain {
		c.shortest(p)
		if c.err != nil {
			return
		}
	}
	if p.Var != "" {
		c.useOrBind(p.Var, info{typ: Path}, p)
	}
	for _, n := range p.Nodes {
		c.node(n, k)
	}
	for _, r := range p.Rels {
		c.rel(r, k)
	}
	c.pathProps(p)
}

func (c *checker) shortest(p *ast.Path) {
	if len(p.Rels) != 1 || len(p.Nodes) != 2 {
		c.fail(gerr.Semantic, "shortest path needs one relationship", p)
		return
	}
	for _, n := range p.Nodes {
		if n == nil || n.Name == "" || !c.bound(n.Name) {
			c.fail(gerr.Semantic, "shortest path endpoint is not bound", n)
			return
		}
	}
	if p.Rels[0] != nil && p.Rels[0].Props != nil {
		c.fail(gerr.Semantic, "property map on shortest path", p.Rels[0])
	}
}

func (c *checker) node(n *ast.NodePat, _ patKind) {
	if n == nil || n.Name == "" {
		return
	}
	c.useOrBind(n.Name, info{typ: Node}, n)
}

func (c *checker) rel(r *ast.RelPat, k patKind) {
	if r == nil {
		return
	}
	if r.VarLen && r.Min < 1 {
		c.fail(gerr.Semantic, "hop lower bound is below 1", r)
		return
	}
	if r.VarLen && r.Max >= 0 && r.Min > r.Max {
		c.fail(gerr.Semantic, "hop lower bound is above the upper bound", r)
		return
	}
	if k != patMatch && len(r.Types) != 1 {
		c.fail(gerr.Semantic, "relationship needs one type", r)
		return
	}
	if r.Name == "" {
		return
	}
	inf := info{typ: Edge}
	if r.VarLen {
		inf = info{typ: List, elem: Edge}
	}
	c.useOrBind(r.Name, inf, r)
}

func (c *checker) pathProps(p *ast.Path) {
	for _, n := range p.Nodes {
		if n != nil && n.Props != nil {
			c.expr(n.Props)
		}
	}
	for _, r := range p.Rels {
		if r != nil && r.Props != nil {
			c.expr(r.Props)
		}
	}
}

func (c *checker) where(e ast.Expr) {
	if e == nil {
		return
	}
	c.expr(e)
}

func (c *checker) unwind(u *ast.Unwind) {
	in := c.expr(u.Expr)
	if in.typ != Any && in.typ != Null && in.typ != List {
		c.fail(gerr.InvalidArgument, "UNWIND expects a list", u.Expr)
		return
	}
	elem := in.elem
	if elem == Any && in.typ != List {
		elem = Any
	}
	c.bindNew(u.Name, info{typ: elem}, u)
}

func (c *checker) with(w *ast.With) {
	cols := c.projection(w.Body)
	if c.err != nil {
		return
	}
	c.install(cols)
	c.where(w.Where)
}

func (c *checker) ret(r *ast.Return) []string {
	cols := c.projection(r.Body)
	return colNames(cols)
}

type column struct {
	name string
	typ  Type
	agg  bool
	key  string
}

func colNames(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.name
	}
	return out
}

func (c *checker) projection(b *ast.ReturnBody) []column {
	if b == nil {
		return nil
	}
	var cols []column
	if b.Star {
		for _, name := range c.names() {
			inf, _ := c.lookup(name)
			cols = append(cols, column{name: name, typ: inf.typ, key: name})
		}
	} else {
		seen := map[string]bool{}
		agg := false
		for _, it := range b.Items {
			itemAgg := c.hasAgg(it.Expr)
			if itemAgg {
				agg = true
			}
			c.mode = modeAgg
			inf := c.expr(it.Expr)
			c.mode = modeValue
			name := it.As
			if name == "" {
				name = ast.Format(it.Expr)
			}
			if seen[name] {
				c.fail(gerr.Semantic, "column "+name+" is already bound", it)
				return nil
			}
			seen[name] = true
			cols = append(cols, column{name: name, typ: inf.typ, agg: itemAgg, key: ast.Format(it.Expr)})
		}
		if agg {
			c.grouping(b.Items, cols)
		}
	}
	c.orderBy(b, cols)
	c.skipLimit(b.Skip)
	c.skipLimit(b.Limit)
	return cols
}

func (c *checker) grouping(items []*ast.Projection, cols []column) {
	keys := map[string]bool{}
	for _, col := range cols {
		if !col.agg {
			keys[col.key] = true
			keys[col.name] = true
		}
	}
	for _, it := range items {
		if !c.hasAgg(it.Expr) {
			continue
		}
		c.rowOutsideAgg(it.Expr, keys, it.Expr)
	}
}

func (c *checker) orderBy(b *ast.ReturnBody, cols []column) {
	if b == nil {
		return
	}
	aliases := map[string]bool{}
	grouped := false
	keys := map[string]bool{}
	for _, col := range cols {
		aliases[col.name] = true
		keys[col.key] = true
		keys[col.name] = true
		if col.agg {
			grouped = true
		}
	}
	for _, s := range b.Order {
		if id, ok := s.Expr.(*ast.Ident); ok && aliases[id.Name] {
			continue
		}
		form := ast.Format(s.Expr)
		if keys[form] {
			mode := modeValue
			for _, col := range cols {
				if col.agg && (col.key == form || col.name == form) {
					mode = modeAgg
				}
			}
			prev := c.mode
			c.mode = mode
			c.expr(s.Expr)
			c.mode = prev
			continue
		}
		if grouped {
			c.fail(gerr.Semantic, "ORDER BY is not a grouping key or alias", s.Expr)
			return
		}
		c.expr(s.Expr)
	}
}

func (c *checker) skipLimit(e ast.Expr) {
	if e == nil {
		return
	}
	c.mode = modeNoRow
	c.expr(e)
	c.mode = modeValue
}

func (c *checker) install(cols []column) {
	m := map[string]info{}
	var names []string
	for _, col := range cols {
		m[col.name] = info{typ: col.typ}
		names = append(names, col.name)
	}
	c.layers = []map[string]info{m}
	c.order = [][]string{names}
}

func (c *checker) set(s *ast.Set) {
	for _, it := range s.Items {
		if _, ok := c.lookup(it.Name); !ok {
			c.fail(gerr.Semantic, "unbound variable "+it.Name, s)
			return
		}
		if it.Value != nil {
			c.expr(it.Value)
		}
	}
}

func (c *checker) remove(r *ast.Remove) {
	for _, it := range r.Items {
		if _, ok := c.lookup(it.Name); !ok {
			c.fail(gerr.Semantic, "unbound variable "+it.Name, r)
			return
		}
	}
}

func (c *checker) del(d *ast.Delete) {
	for _, e := range d.Exprs {
		inf := c.expr(e)
		if c.err != nil {
			return
		}
		if inf.typ != Any && inf.typ != Null && inf.typ != Node && inf.typ != Edge && inf.typ != Entity {
			c.fail(gerr.InvalidArgument, "DELETE expects a node or an edge", e)
		}
	}
}

func (c *checker) call(n *ast.Call) {
	for _, a := range n.Args {
		c.expr(a)
	}
	cols, ok := procCols(n.Proc)
	if !ok {
		c.fail(gerr.Semantic, "unknown procedure "+strings.Join(n.Proc, "."), n)
		return
	}
	if len(n.Yield) == 0 {
		if n.Where != nil {
			c.push()
			for _, name := range cols {
				c.bindNew(name, info{typ: String}, n)
			}
			c.where(n.Where)
			c.pop()
		}
		return
	}
	known := map[string]bool{}
	for _, name := range cols {
		known[name] = true
	}
	for _, y := range n.Yield {
		if !known[y.Name] {
			c.fail(gerr.Semantic, "unknown yield "+y.Name, n)
			return
		}
		out := y.As
		if out == "" {
			out = y.Name
		}
		c.bindNew(out, info{typ: String}, n)
	}
	c.where(n.Where)
}

func (c *checker) callCols(n *ast.Call) []string {
	if len(n.Yield) > 0 {
		out := make([]string, len(n.Yield))
		for i, y := range n.Yield {
			if y.As != "" {
				out[i] = y.As
			} else {
				out[i] = y.Name
			}
		}
		return out
	}
	cols, _ := procCols(n.Proc)
	return cols
}

func procCols(parts []string) ([]string, bool) {
	name := strings.ToLower(strings.Join(parts, "."))
	switch name {
	case "db.labels":
		return []string{"label"}, true
	case "db.edgetypes":
		return []string{"edgeType"}, true
	case "db.propertykeys":
		return []string{"propertyKey"}, true
	default:
		return nil, false
	}
}

func (c *checker) createIndex(ix *ast.CreateIndex) {
	if ix.For == nil || len(ix.Props) == 0 {
		c.fail(gerr.Semantic, "index needs a property", ix)
		return
	}
	for _, p := range ix.Props {
		if p.Var != ix.For.Var {
			c.fail(gerr.Semantic, "index variable "+p.Var+" does not match "+ix.For.Var, ix)
			return
		}
	}
}

func (c *checker) createConstraint(con *ast.CreateConstraint) {
	if con.For == nil || con.Var != con.For.Var {
		c.fail(gerr.Semantic, "constraint variable does not match the pattern", con)
		return
	}
	if con.Prop == "" {
		c.fail(gerr.Semantic, "constraint needs a property", con)
		return
	}
	if con.Kind == ast.ConstraintType {
		if _, ok := lookupTypeName(con.Type); !ok {
			c.fail(gerr.Semantic, "unknown type "+con.Type, con)
		}
	}
}

var dbName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func (c *checker) database(name string, createOrDrop bool, n ast.Node) {
	if !dbName.MatchString(name) {
		c.fail(gerr.Semantic, "database name "+name, n)
		return
	}
	if createOrDrop && (strings.EqualFold(name, "system") || strings.EqualFold(name, "default")) {
		c.fail(gerr.Semantic, "database name "+name+" is reserved", n)
	}
}

func (c *checker) namesInStmt(n ast.Node) {
	if n == nil {
		return
	}
	ast.Walk(stmtVars{c: c}, n)
}

type stmtVars struct{ c *checker }

func (v stmtVars) Visit(n ast.Node) ast.Visitor {
	if v.c.err != nil || n == nil {
		return nil
	}
	if e, ok := n.(ast.Expr); ok {
		v.c.expr(e)
		return nil
	}
	return v
}

func (c *checker) bound(name string) bool {
	_, ok := c.lookup(name)
	return ok
}

func (c *checker) lookup(name string) (info, bool) {
	for i := len(c.layers) - 1; i >= 0; i-- {
		if inf, ok := c.layers[i][name]; ok {
			return inf, true
		}
	}
	return info{}, false
}

func (c *checker) names() []string {
	return c.order[len(c.order)-1]
}

func (c *checker) bindNew(name string, inf info, at ast.Node) {
	if name == "" {
		return
	}
	top := c.layers[len(c.layers)-1]
	if _, ok := top[name]; ok {
		c.fail(gerr.Semantic, name+" is already bound", at)
		return
	}
	top[name] = inf
	c.order[len(c.order)-1] = append(c.order[len(c.order)-1], name)
}

func (c *checker) useOrBind(name string, inf info, at ast.Node) {
	if name == "" {
		return
	}
	if got, ok := c.lookup(name); ok {
		if !compatible(got.typ, inf.typ) {
			c.fail(gerr.Semantic, name+" is already bound as "+got.typ.String(), at)
		}
		return
	}
	c.bindNew(name, inf, at)
}

func (c *checker) push() {
	c.layers = append(c.layers, map[string]info{})
	c.order = append(c.order, nil)
}

func (c *checker) pop() {
	c.layers = c.layers[:len(c.layers)-1]
	c.order = c.order[:len(c.order)-1]
}

func (c *checker) fail(code gerr.Code, msg string, n ast.Node) {
	if c.err != nil {
		return
	}
	line, col := c.loc(n)
	c.err = gerr.Newf(code, "%s at %d:%d", msg, line, col)
}

func (c *checker) loc(n ast.Node) (int, int) {
	if n == nil {
		return 1, 1
	}
	if p, ok := c.pos[n]; ok {
		return p.Line, p.Col
	}
	line, col := 0, 0
	ast.Walk(&posFind{c: c, line: &line, col: &col}, n)
	if line == 0 {
		return 1, 1
	}
	return line, col
}

type posFind struct {
	c         *checker
	line, col *int
}

func (f *posFind) Visit(n ast.Node) ast.Visitor {
	if n == nil || *f.line != 0 {
		return nil
	}
	if p, ok := f.c.pos[n]; ok {
		*f.line, *f.col = p.Line, p.Col
		return nil
	}
	return f
}

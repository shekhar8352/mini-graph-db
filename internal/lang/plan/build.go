package plan

import (
	"sort"
	"strconv"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/lang/sema"
)

// Build checks src and returns its logical plan.
// A semantic or syntax error is returned unchanged.
func Build(src string) (*Plan, error) {
	return BuildWith(src, nil)
}

// BuildWith plans src using catalog statistics and declared indexes when sch is set.
func BuildWith(src string, sch *catalog.Schema) (*Plan, error) {
	if err := sema.Check(src); err != nil {
		return nil, err
	}
	script, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	p := &Plan{}
	if script == nil {
		return p, nil
	}
	for _, st := range script.Stmts {
		op, err := BuildStmt(st, sch)
		if err != nil {
			return nil, err
		}
		p.Stmts = append(p.Stmts, op)
	}
	return p, nil
}

// BuildStmt plans one statement that already passed the semantic checker.
func BuildStmt(st ast.Stmt, sch *catalog.Schema) (Op, error) {
	op, err := buildStmt(st, sch)
	if err != nil {
		return nil, err
	}
	return optimize(op, sch), nil
}

func buildStmt(st ast.Stmt, sch *catalog.Schema) (Op, error) {
	switch s := st.(type) {
	case *ast.Query:
		return buildQuery(s, sch), nil
	case *ast.Explain:
		return buildQuery(s.Query, sch), nil
	case *ast.Profile:
		return buildQuery(s.Query, sch), nil
	case *ast.Begin:
		if s.ReadOnly {
			return &Leaf{Name: "Begin", Detail: "READ ONLY"}, nil
		}
		return &Leaf{Name: "Begin"}, nil
	case *ast.Commit:
		return &Leaf{Name: "Commit"}, nil
	case *ast.Rollback:
		return &Leaf{Name: "Rollback"}, nil
	case *ast.Vacuum:
		return &Leaf{Name: "Vacuum"}, nil
	case *ast.Analyze:
		return &Leaf{Name: "Analyze"}, nil
	case *ast.Use:
		return &Leaf{Name: "Use", Detail: s.Name}, nil
	case *ast.Terminate:
		return &Leaf{Name: "Terminate", Detail: leafDetail(s.ID)}, nil
	case *ast.Show:
		detail := string(s.Kind)
		if s.Name != "" {
			detail += " FOR " + s.Name
		}
		return &Leaf{Name: "Show", Detail: detail}, nil
	case *ast.CreateIndex:
		return &Leaf{Name: "CreateIndex", Detail: s.Name}, nil
	case *ast.DropIndex:
		return &Leaf{Name: "DropIndex", Detail: s.Name}, nil
	case *ast.CreateConstraint:
		return &Leaf{Name: "CreateConstraint", Detail: s.Name}, nil
	case *ast.DropConstraint:
		return &Leaf{Name: "DropConstraint", Detail: s.Name}, nil
	case *ast.CreateDatabase:
		return &Leaf{Name: "CreateDatabase", Detail: s.Name}, nil
	case *ast.DropDatabase:
		return &Leaf{Name: "DropDatabase", Detail: s.Name}, nil
	case *ast.CreateUser:
		return &Leaf{Name: "CreateUser", Detail: s.Name}, nil
	case *ast.AlterUser:
		return &Leaf{Name: "AlterUser", Detail: s.Name}, nil
	case *ast.DropUser:
		return &Leaf{Name: "DropUser", Detail: s.Name}, nil
	case *ast.CreateRole:
		return &Leaf{Name: "CreateRole", Detail: s.Name}, nil
	case *ast.DropRole:
		return &Leaf{Name: "DropRole", Detail: s.Name}, nil
	case *ast.GrantRole:
		return &Leaf{Name: "GrantRole", Detail: s.Role + " TO " + s.To}, nil
	case *ast.RevokeRole:
		return &Leaf{Name: "RevokeRole", Detail: s.Role + " FROM " + s.From}, nil
	case *ast.GrantPriv:
		return &Leaf{Name: "Grant", Detail: privText(string(s.Priv), s.Database, "TO", s.To)}, nil
	case *ast.RevokePriv:
		return &Leaf{Name: "Revoke", Detail: privText(string(s.Priv), s.Database, "FROM", s.From)}, nil
	case *ast.DenyPriv:
		return &Leaf{Name: "Deny", Detail: privText(string(s.Priv), s.Database, "TO", s.To)}, nil
	case *ast.CreateToken:
		return &Leaf{Name: "CreateToken", Detail: s.User + ", " + ast.Format(s.Expires)}, nil
	case *ast.RevokeToken:
		return &Leaf{Name: "RevokeToken", Detail: strconv.FormatInt(s.ID, 10)}, nil
	default:
		return nil, gerr.New(gerr.Internal, "unknown statement")
	}
}

func privText(priv, db, rel, who string) string {
	return priv + " ON DATABASE " + db + " " + rel + " " + who
}

func buildQuery(q *ast.Query, sch *catalog.Schema) Op {
	if q == nil || len(q.Parts) == 0 {
		return nil
	}
	var cur Op
	for i, part := range q.Parts {
		side := newBld(sch).single(part)
		if i == 0 {
			cur = side
			continue
		}
		all := false
		if i-1 < len(q.All) {
			all = q.All[i-1]
		}
		cur = &Union{Left: cur, Right: side, All: all}
	}
	return cur
}

type bld struct {
	bound map[string]bool
	order []string
	sch   *catalog.Schema
}

func newBld(sch *catalog.Schema) *bld {
	return &bld{bound: map[string]bool{}, sch: sch}
}

func (b *bld) bind(name string) {
	if name == "" || b.bound[name] {
		return
	}
	b.bound[name] = true
	b.order = append(b.order, name)
}

func (b *bld) install(names []string) {
	b.bound = map[string]bool{}
	b.order = nil
	for _, name := range names {
		b.bind(name)
	}
}

func (b *bld) single(q *ast.SingleQuery) Op {
	if q == nil {
		return nil
	}
	var cur Op
	for _, cl := range q.Clauses {
		cur = b.clause(cur, cl)
	}
	return cur
}

func (b *bld) clause(in Op, cl ast.Clause) Op {
	switch n := cl.(type) {
	case *ast.Match:
		return b.match(in, n)
	case *ast.Unwind:
		b.bind(n.Name)
		return &Unwind{Input: in, Expr: n.Expr, Name: n.Name}
	case *ast.With:
		return b.tail(in, n.Body, n.Where)
	case *ast.Return:
		return b.tail(in, n.Body, nil)
	case *ast.Create:
		b.bindPattern(n.Pattern)
		return &Create{Input: in, Pattern: n.Pattern}
	case *ast.Merge:
		b.bindPath(n.Path)
		return &Merge{Input: in, Path: n.Path, OnCreate: n.OnCreate, OnMatch: n.OnMatch}
	case *ast.Set:
		return b.set(in, n)
	case *ast.Remove:
		return &Remove{Input: in, Items: n.Items}
	case *ast.Delete:
		return &Delete{Input: in, Exprs: n.Exprs, Detach: n.Detach}
	case *ast.Call:
		return b.call(in, n)
	default:
		return in
	}
}

func (b *bld) match(in Op, m *ast.Match) Op {
	before := append([]string(nil), b.order...)
	right := b.pattern(nil, m.Pattern)
	have := map[string]bool{}
	for _, name := range before {
		have[name] = true
	}
	var fresh []string
	for _, name := range b.order {
		if !have[name] {
			fresh = append(fresh, name)
		}
	}
	if m.Where != nil {
		right = &Filter{Input: right, Expr: m.Where}
	}
	if m.Optional {
		right = &Optional{Input: right, Names: fresh}
	}
	if in == nil {
		return right
	}
	return &Apply{Left: in, Right: right, NewMatch: true}
}

func (b *bld) pattern(in Op, p *ast.Pattern) Op {
	if p == nil {
		return in
	}
	paths := p.Paths
	if in == nil && b.sch != nil && disconnectedNodes(paths) {
		paths = orderPaths(paths, b.sch)
	}
	for _, path := range paths {
		in = b.path(in, path)
	}
	return in
}

func disconnectedNodes(paths []*ast.Path) bool {
	if len(paths) < 2 {
		return false
	}
	for _, p := range paths {
		if p == nil || p.Fn != ast.PathPlain || len(p.Rels) > 0 || len(p.Nodes) != 1 {
			return false
		}
	}
	return true
}

func orderPaths(paths []*ast.Path, sch *catalog.Schema) []*ast.Path {
	out := append([]*ast.Path(nil), paths...)
	sort.SliceStable(out, func(i, j int) bool {
		return nodeCost(out[i].Nodes[0], sch) < nodeCost(out[j].Nodes[0], sch)
	})
	return out
}

func nodeCost(n *ast.NodePat, sch *catalog.Schema) float64 {
	if n == nil {
		return sch.EstimateAll()
	}
	if seekable(n) {
		if len(n.Labels) == 1 && len(n.Labels[0]) == 1 && n.Props != nil && len(n.Props.Entries) > 0 {
			return sch.EstimateEq(n.Labels[0][0], n.Props.Entries[0].Key)
		}
		return 1
	}
	if len(n.Labels) > 0 && len(n.Labels[0]) > 0 {
		return sch.EstimateLabel(n.Labels[0][0])
	}
	return sch.EstimateAll()
}

func (b *bld) path(in Op, p *ast.Path) Op {
	if p == nil {
		return in
	}
	if p.Fn != ast.PathPlain {
		b.bind(p.Var)
		for _, r := range p.Rels {
			if r != nil {
				b.bind(r.Name)
			}
		}
		return &ShortestPath{Input: in, Path: p}
	}
	if len(p.Nodes) == 0 {
		return in
	}
	if len(p.Rels) == 0 {
		op := b.placeNode(in, p.Nodes[0])
		b.bind(p.Var)
		return withPath(op, p.Var)
	}
	cur := b.placeNode(in, p.Nodes[0])
	for i, rel := range p.Rels {
		pathName := ""
		if i == len(p.Rels)-1 {
			pathName = p.Var
		}
		cur = b.placeExpand(cur, p.Nodes[i], rel, p.Nodes[i+1], pathName)
	}
	b.bind(p.Var)
	return cur
}

func (b *bld) placeNode(in Op, n *ast.NodePat) Op {
	if n == nil {
		return in
	}
	if n.Name != "" && b.bound[n.Name] {
		if len(n.Labels) > 0 || n.Props != nil {
			return &Filter{Input: in, Node: n}
		}
		return in
	}
	leaf := b.scanLeaf(n)
	b.bind(n.Name)
	if in == nil {
		return leaf
	}
	return &Apply{Left: in, Right: leaf}
}

func seekable(n *ast.NodePat) bool {
	if n == nil || n.Props == nil || len(n.Props.Entries) == 0 {
		return false
	}
	return len(n.Labels) == 1 && len(n.Labels[0]) == 1
}

func (b *bld) scanLeaf(n *ast.NodePat) Op {
	if n == nil {
		return &NodeScan{Node: n}
	}
	label := ""
	if len(n.Labels) > 0 && len(n.Labels[0]) == 1 {
		label = n.Labels[0][0]
	}
	eq := map[string]ast.Expr{}
	if n.Props != nil {
		for _, ent := range n.Props.Entries {
			eq[ent.Key] = ent.Value
		}
	}
	if ix, ok := pickIndex(b.sch, label, eq, "", nil, nil, nil); ok {
		return &IndexSeek{Node: n, Name: ix.Name, Op: "="}
	}
	if seekable(n) {
		return &IndexSeek{Node: n}
	}
	if len(n.Labels) > 0 {
		return &LabelScan{Node: n}
	}
	return &NodeScan{Node: n}
}

func (b *bld) placeExpand(in Op, from *ast.NodePat, rel *ast.RelPat, to *ast.NodePat, path string) Op {
	fromName := ""
	if from != nil {
		fromName = from.Name
	}
	hop := &ast.Path{
		Nodes: []*ast.NodePat{{Name: fromName}, to},
		Rels:  []*ast.RelPat{rel},
	}
	if rel != nil {
		b.bind(rel.Name)
	}
	if to != nil {
		b.bind(to.Name)
	}
	b.bind(path)
	return &Expand{Input: in, Hop: hop, Path: path}
}

func withPath(op Op, name string) Op {
	if name == "" || op == nil {
		return op
	}
	switch n := op.(type) {
	case *Apply:
		n.Right = withPath(n.Right, name)
	case *NodeScan:
		n.Path = name
	case *LabelScan:
		n.Path = name
	case *IndexSeek:
		n.Path = name
	case *Filter:
		n.Input = withPath(n.Input, name)
	}
	return op
}

func (b *bld) tail(in Op, body *ast.ReturnBody, where ast.Expr) Op {
	if body == nil {
		return in
	}
	in = b.project(in, body)
	if body.Distinct {
		in = &Distinct{Input: in}
	}
	if where != nil {
		in = &Filter{Input: in, Expr: where}
	}
	if len(body.Order) > 0 {
		in = &Sort{Input: in, Keys: body.Order}
	}
	if body.Skip != nil {
		in = &Skip{Input: in, Count: body.Skip}
	}
	if body.Limit != nil {
		in = &Limit{Input: in, Count: body.Limit}
	}
	return in
}

func (b *bld) project(in Op, body *ast.ReturnBody) Op {
	if body.Star {
		names := append([]string(nil), b.order...)
		b.install(names)
		return &Project{Input: in, Star: names}
	}
	agg := false
	names := make([]string, len(body.Items))
	for i, it := range body.Items {
		if itemAgg(it.Expr) {
			agg = true
		}
		if it.As != "" {
			names[i] = it.As
			continue
		}
		names[i] = ast.Format(it.Expr)
	}
	b.install(names)
	if agg {
		return &Aggregate{Input: in, Items: body.Items}
	}
	return &Project{Input: in, Items: body.Items}
}

func (b *bld) call(in Op, c *ast.Call) Op {
	var op Op = &Leaf{Name: "Call", Detail: callText(c)}
	if len(c.Yield) > 0 {
		items := make([]*ast.Projection, len(c.Yield))
		names := make([]string, len(c.Yield))
		for i, y := range c.Yield {
			as := y.As
			if as == "" {
				as = y.Name
			}
			items[i] = &ast.Projection{Expr: &ast.Ident{Name: y.Name}, As: y.As}
			names[i] = as
		}
		op = &Project{Input: op, Items: items}
		b.install(names)
	}
	if c.Where != nil {
		op = &Filter{Input: op, Expr: c.Where}
	}
	if in == nil {
		return op
	}
	return &Apply{Left: in, Right: op}
}

func (b *bld) set(in Op, s *ast.Set) Op {
	var props, labels []*ast.SetItem
	flush := func() {
		if len(props) > 0 {
			in = &SetProps{Input: in, Items: props}
			props = nil
		}
		if len(labels) > 0 {
			in = &SetLabels{Input: in, Items: labels}
			labels = nil
		}
	}
	var mode string
	for _, it := range s.Items {
		kind := "prop"
		if it.Op == ast.SetLabels {
			kind = "label"
		}
		if mode != "" && mode != kind {
			flush()
		}
		mode = kind
		if kind == "label" {
			labels = append(labels, it)
			continue
		}
		props = append(props, it)
	}
	flush()
	return in
}

func (b *bld) bindPattern(p *ast.Pattern) {
	if p == nil {
		return
	}
	for _, path := range p.Paths {
		b.bindPath(path)
	}
}

func (b *bld) bindPath(p *ast.Path) {
	if p == nil {
		return
	}
	b.bind(p.Var)
	for _, n := range p.Nodes {
		if n != nil {
			b.bind(n.Name)
		}
	}
	for _, r := range p.Rels {
		if r != nil {
			b.bind(r.Name)
		}
	}
}

func itemAgg(e ast.Expr) bool {
	v := &aggVisitor{}
	ast.Walk(v, e)
	return v.found
}

type aggVisitor struct{ found bool }

func (v *aggVisitor) Visit(n ast.Node) ast.Visitor {
	if v.found || n == nil {
		return nil
	}
	if c, ok := n.(*ast.CallExpr); ok && aggName(c.Name) {
		v.found = true
		return nil
	}
	return v
}

func aggName(name string) bool {
	switch strings.ToLower(name) {
	case "count", "sum", "avg", "min", "max", "collect", "percentiledisc", "stdev":
		return true
	default:
		return false
	}
}

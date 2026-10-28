// Package exec runs a GQL-lite script through Volcano iterators.
// Each operator yields one row at a time. Sort, aggregate, and distinct
// count their buffered rows against the per-query memory budget.
// Each statement is atomic inside the open transaction. In auto-commit the
// statement has a transaction of its own. EXPLAIN returns the plan text.
// PROFILE runs the query and records rows, time, and db hits per operator.
// A panic in Run becomes Internal. The shell compiles a line with
// internal/compat/legacy and then calls Run.
package exec

import (
	"context"
	"fmt"

	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/exec/expr"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/lang/plan"
	"github.com/shekhar8352/mini-graph-db/internal/lang/sema"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

const (
	defaultBudget int64 = 8 << 20
	defaultHops         = 128
)

// Options configures one execution.
// A zero Budget selects 8 MiB. A zero MaxHops selects 128.
// Notifications are copied onto every statement result.
type Options struct {
	Params        map[string]value.Value
	Budget        int64
	MaxHops       int
	Now           func() value.Value
	Rand          func() float64
	Notifications []Notification
}

// Run plans src and pulls every operator to completion.
// It returns the result of the last statement.
// A writing statement with no RETURN yields no rows and a summary.
// A failed statement drops its own writes. An explicit transaction stays open.
// In auto-commit the statement's transaction is rolled back, and earlier
// statements in the script stay committed. An error stops the script.
// EXPLAIN returns the plan text and does not run the query.
// PROFILE runs the query and fills Result.Profile.
func Run(ctx context.Context, g *graph.Graph, src string, opt Options) (res *Result, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			res = nil
			err = gerr.Newf(gerr.Internal, "panic: %v", rec)
		}
	}()
	if g == nil {
		return nil, gerr.New(gerr.InvalidArgument, "no graph")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sema.Check(src); err != nil {
		return nil, err
	}
	script, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	eng := newEngine(g, opt)
	if err := eng.refresh(); err != nil {
		return nil, err
	}
	var stmts []ast.Stmt
	if script != nil {
		stmts = script.Stmts
	}
	res = &Result{}
	for _, st := range stmts {
		sch, err := g.Schema()
		if err != nil {
			return nil, err
		}
		op, err := plan.BuildStmt(st, sch)
		if err != nil {
			return nil, err
		}
		res, err = eng.runStmt(ctx, st, op)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (e *engine) runStmt(ctx context.Context, st ast.Stmt, op plan.Op) (*Result, error) {
	e.sum = Summary{}
	e.profile = false
	e.ops = nil
	e.seen = nil
	e.cur = nil
	if txnControl(st) {
		return e.runOp(ctx, op)
	}
	if e.g.InTransaction() {
		mark, err := e.g.Mark()
		if err != nil {
			return nil, err
		}
		res, err := e.runBody(ctx, st, op)
		if err != nil {
			if rb := e.g.RollbackTo(mark); rb != nil {
				return nil, rb
			}
			return nil, err
		}
		return res, nil
	}
	if err := e.g.Begin(false); err != nil {
		return nil, err
	}
	res, err := e.runBody(ctx, st, op)
	if err != nil {
		_ = e.g.Rollback()
		return nil, err
	}
	if err := e.g.Commit(); err != nil {
		_ = e.g.Rollback()
		return nil, err
	}
	return res, nil
}

func (e *engine) runBody(ctx context.Context, st ast.Stmt, op plan.Op) (*Result, error) {
	switch s := st.(type) {
	case *ast.Explain:
		return e.explain(op), nil
	case *ast.Profile:
		e.profile = true
		return e.runOp(ctx, op)
	case *ast.CreateIndex:
		if err := e.createIndex(s); err != nil {
			return nil, err
		}
		e.sum.IndexesAdded++
		return e.result(nil, nil), nil
	case *ast.DropIndex:
		if err := e.g.DropIndex(s.Name); err != nil {
			return nil, err
		}
		e.sum.IndexesRemoved++
		return e.result(nil, nil), nil
	case *ast.CreateConstraint:
		if err := e.createConstraint(s); err != nil {
			return nil, err
		}
		e.sum.ConstraintsAdded++
		if s.Kind == ast.ConstraintUnique {
			e.sum.IndexesAdded++
		}
		return e.result(nil, nil), nil
	case *ast.DropConstraint:
		if err := e.g.DropConstraint(s.Name); err != nil {
			return nil, err
		}
		e.sum.ConstraintsRemoved++
		return e.result(nil, nil), nil
	case *ast.Analyze:
		if err := e.g.Analyze(); err != nil {
			return nil, err
		}
		return e.result(nil, nil), nil
	}
	return e.runOp(ctx, op)
}

func (e *engine) createIndex(s *ast.CreateIndex) error {
	if s.For == nil {
		return gerr.New(gerr.InvalidArgument, "index needs a pattern")
	}
	props := make([]string, 0, len(s.Props))
	for _, p := range s.Props {
		if p != nil {
			props = append(props, p.Prop)
		}
	}
	return e.g.CreateIndex(s.Name, s.For.Edge, s.For.On, props, false)
}

func (e *engine) createConstraint(s *ast.CreateConstraint) error {
	if s.For == nil {
		return gerr.New(gerr.InvalidArgument, "constraint needs a pattern")
	}
	kind := catalog.UniqueConstraint
	switch s.Kind {
	case ast.ConstraintExists:
		kind = catalog.ExistsConstraint
	case ast.ConstraintType:
		kind = catalog.TypeConstraint
	}
	return e.g.CreateConstraint(s.Name, s.For.Edge, s.For.On, s.Prop, kind, strings.ToUpper(s.Type))
}

func (e *engine) explain(op plan.Op) *Result {
	text := plan.Format(op)
	return e.result([]string{"plan"}, [][]value.Value{{value.String(text)}})
}

func txnControl(st ast.Stmt) bool {
	switch st.(type) {
	case *ast.Begin, *ast.Commit, *ast.Rollback:
		return true
	default:
		return false
	}
}

type engine struct {
	g       *graph.Graph
	view    *graphView
	params  map[string]value.Value
	mem     *budget
	hops    int
	now     func() value.Value
	rand    func() float64
	notes   []Notification
	sum     Summary
	profile bool
	ops     []*Operator
	seen    map[plan.Op]*Operator
	cur     *Operator
}

func newEngine(g *graph.Graph, opt Options) *engine {
	limit := opt.Budget
	if limit <= 0 {
		limit = defaultBudget
	}
	hops := opt.MaxHops
	if hops <= 0 {
		hops = defaultHops
	}
	return &engine{
		g:      g,
		params: opt.Params,
		mem:    &budget{limit: limit},
		hops:   hops,
		now:    opt.Now,
		rand:   opt.Rand,
		notes:  append([]Notification(nil), opt.Notifications...),
	}
}

func (e *engine) runOp(ctx context.Context, op plan.Op) (*Result, error) {
	if e.profile {
		e.indexPlan(op, 0)
	}
	it := e.open(op, blank())
	cols := outputCols(op)
	keep := yields(op)
	var rows [][]value.Value
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, ok, err := it.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if keep {
			rows = append(rows, take(cols, r))
		}
	}
	if !keep {
		return e.result(nil, nil), nil
	}
	return e.result(cols, rows), nil
}

func (e *engine) result(cols []string, rows [][]value.Value) *Result {
	res := &Result{
		Columns:       cols,
		rows:          rows,
		Summary:       e.sum,
		Notifications: append([]Notification(nil), e.notes...),
	}
	if e.profile {
		res.Profile = make([]Operator, len(e.ops))
		for i, op := range e.ops {
			res.Profile[i] = *op
		}
	}
	e.sum = Summary{}
	e.ops = nil
	e.seen = nil
	e.cur = nil
	return res
}

func (e *engine) env(r row) *expr.Env {
	env := &expr.Env{Params: e.params, Graph: e.view, Now: e.now, Rand: e.rand}
	for k, v := range r.val {
		env.Set(k, v)
	}
	return env
}

func (e *engine) refresh() error {
	nodes := e.g.AllNodes()
	e.hit(int64(len(nodes)))
	edgesIn := e.g.AllEdges()
	e.hit(int64(len(edgesIn)))
	v := &graphView{
		eng:   e,
		nodes: map[uint64]expr.NodeRec{},
		edges: map[uint64]expr.EdgeRec{},
		out:   map[uint64][]expr.EdgeRec{},
		in:    map[uint64][]expr.EdgeRec{},
	}
	for _, n := range nodes {
		v.nodes[n.ID] = expr.NodeRec{ID: n.ID, Labels: n.Labels(), Props: n.Properties()}
	}
	var edges []expr.EdgeRec
	for _, ed := range edgesIn {
		edges = append(edges, expr.EdgeRec{
			ID: ed.ID, From: ed.From, To: ed.To, Type: ed.Label, Props: ed.Properties(),
		})
	}
	for _, ed := range edges {
		v.edges[ed.ID] = ed
		v.out[ed.From] = append(v.out[ed.From], ed)
		v.in[ed.To] = append(v.in[ed.To], ed)
	}
	for id, xs := range v.out {
		sortEdges(xs)
		v.out[id] = xs
	}
	for id, xs := range v.in {
		sortEdges(xs)
		v.in[id] = xs
	}
	e.view = v
	return nil
}

type graphView struct {
	eng   *engine
	nodes map[uint64]expr.NodeRec
	edges map[uint64]expr.EdgeRec
	out   map[uint64][]expr.EdgeRec
	in    map[uint64][]expr.EdgeRec
}

func (v *graphView) Node(id uint64) (expr.NodeRec, bool) {
	if v == nil {
		return expr.NodeRec{}, false
	}
	v.eng.hit(1)
	n, ok := v.nodes[id]
	return n, ok
}

func (v *graphView) Edge(id uint64) (expr.EdgeRec, bool) {
	if v == nil {
		return expr.EdgeRec{}, false
	}
	v.eng.hit(1)
	ed, ok := v.edges[id]
	return ed, ok
}

func (v *graphView) Out(id uint64) []expr.EdgeRec {
	if v == nil {
		return nil
	}
	v.eng.hit(1)
	return append([]expr.EdgeRec(nil), v.out[id]...)
}

func (v *graphView) In(id uint64) []expr.EdgeRec {
	if v == nil {
		return nil
	}
	v.eng.hit(1)
	return append([]expr.EdgeRec(nil), v.in[id]...)
}

type budget struct {
	limit int64
	used  int64
}

func (b *budget) add(n int64) error {
	if n < 0 {
		n = 0
	}
	if b.used+n > b.limit {
		return gerr.New(gerr.ResourceExhausted, "query memory budget")
	}
	b.used += n
	return nil
}

type row struct {
	val    map[string]value.Value
	used   map[uint64]struct{}
	trailN []uint64
	trailE []uint64
}

func blank() row {
	return row{val: map[string]value.Value{}, used: map[uint64]struct{}{}}
}

func (r row) clone() row {
	out := blank()
	for k, v := range r.val {
		out.val[k] = v
	}
	for id := range r.used {
		out.used[id] = struct{}{}
	}
	out.trailN = append([]uint64(nil), r.trailN...)
	out.trailE = append([]uint64(nil), r.trailE...)
	return out
}

type iterator interface {
	Next(ctx context.Context) (row, bool, error)
}

type unitIter struct {
	row  row
	done bool
}

func (u *unitIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if u.done {
		return row{}, false, nil
	}
	u.done = true
	return u.row, true, nil
}

func outputCols(op plan.Op) []string {
	switch n := op.(type) {
	case *plan.Project:
		return projCols(n.Items, n.Star)
	case *plan.Aggregate:
		return projCols(n.Items, nil)
	case *plan.Limit:
		return outputCols(n.Input)
	case *plan.Skip:
		return outputCols(n.Input)
	case *plan.Sort:
		return outputCols(n.Input)
	case *plan.Distinct:
		return outputCols(n.Input)
	case *plan.Filter:
		return outputCols(n.Input)
	case *plan.Optional:
		return outputCols(n.Input)
	case *plan.Union:
		return outputCols(n.Left)
	case *plan.Leaf:
		switch n.Name {
		case "Call":
			return callCols(n.Detail)
		case "Show":
			return showCols(n.Detail)
		case "Vacuum":
			return []string{"reclaimed"}
		}
	}
	return nil
}

func yields(op plan.Op) bool {
	switch n := op.(type) {
	case *plan.Project, *plan.Aggregate, *plan.Union:
		return true
	case *plan.Leaf:
		return n.Name == "Call" || n.Name == "Show" || n.Name == "Vacuum"
	case *plan.Limit:
		return yields(n.Input)
	case *plan.Skip:
		return yields(n.Input)
	case *plan.Sort:
		return yields(n.Input)
	case *plan.Distinct:
		return yields(n.Input)
	case *plan.Filter:
		return yields(n.Input)
	case *plan.Optional:
		return yields(n.Input)
	default:
		return false
	}
}

func projCols(items []*ast.Projection, star []string) []string {
	if len(star) > 0 {
		return append([]string(nil), star...)
	}
	out := make([]string, len(items))
	for i, it := range items {
		if it.As != "" {
			out[i] = it.As
			continue
		}
		out[i] = ast.Format(it.Expr)
	}
	return out
}

func callCols(detail string) []string {
	switch detail {
	case "db.labels()":
		return []string{"label"}
	case "db.edgeTypes()":
		return []string{"edgeType"}
	case "db.propertyKeys()":
		return []string{"propertyKey"}
	default:
		return nil
	}
}

func boundNode(r row, name string) (uint64, bool) {
	v, ok := r.val[name]
	if !ok {
		return 0, false
	}
	return v.NodeID()
}

func take(cols []string, r row) []value.Value {
	out := make([]value.Value, len(cols))
	for i, c := range cols {
		v, ok := r.val[c]
		if !ok {
			v = value.Null()
		}
		out[i] = v
	}
	return out
}

func rowBytes(r row) int64 {
	var n int64
	for k, v := range r.val {
		n += int64(len(k)) + nbytes(v)
	}
	return n + 1
}

func nbytes(v value.Value) int64 {
	n := int64(8)
	switch v.Kind() {
	case value.KindString:
		s, _ := v.StringValue()
		n += int64(len(s))
	case value.KindBytes:
		b, _ := v.BytesValue()
		n += int64(len(b))
	case value.KindList:
		xs, _ := v.ListValue()
		for _, el := range xs {
			n += nbytes(el)
		}
	case value.KindMap:
		m, _ := v.MapValue()
		for k, el := range m {
			n += int64(len(k)) + nbytes(el)
		}
	}
	return n
}

func sortEdges(xs []expr.EdgeRec) {
	for i := 1; i < len(xs); i++ {
		j := i
		for j > 0 && xs[j].ID < xs[j-1].ID {
			xs[j], xs[j-1] = xs[j-1], xs[j]
			j--
		}
	}
}

func copySet(in map[uint64]struct{}) map[uint64]struct{} {
	out := make(map[uint64]struct{}, len(in))
	for id := range in {
		out[id] = struct{}{}
	}
	return out
}

func sameRow(a, b row) bool {
	if len(a.val) != len(b.val) {
		return false
	}
	for k, v := range a.val {
		w, ok := b.val[k]
		if !ok || !value.Equal(v, w) {
			return false
		}
	}
	return true
}

func aggFn(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max", "collect", "percentiledisc", "stdev":
		return true
	default:
		return false
	}
}

func slotName(i int) string { return fmt.Sprintf("\x00agg%d", i) }

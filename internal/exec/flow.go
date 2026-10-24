package exec

import (
	"context"
	"sort"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/exec/expr"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/lang/plan"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func (e *engine) openPlan(op plan.Op, seed row) iterator {
	if op == nil {
		return &unitIter{row: seed}
	}
	switch n := op.(type) {
	case *plan.NodeScan:
		return &scanIter{eng: e, mode: modeAll, node: n.Node, path: n.Path, seed: seed}
	case *plan.LabelScan:
		return &scanIter{eng: e, mode: modeLabel, node: n.Node, path: n.Path, seed: seed}
	case *plan.IndexSeek:
		return &scanIter{eng: e, mode: modeSeek, node: n.Node, path: n.Path, seed: seed}
	case *plan.Expand:
		return newExpand(e, n, e.open(n.Input, seed))
	case *plan.Filter:
		return &filterIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Project:
		return &projectIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Aggregate:
		return &aggIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Sort:
		return &sortIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Limit:
		return &limitIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Skip:
		return &skipIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Distinct:
		return &distinctIter{eng: e, in: e.open(n.Input, seed)}
	case *plan.Union:
		return &unionIter{eng: e, op: n, left: e.open(n.Left, blank())}
	case *plan.Unwind:
		return &unwindIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Optional:
		return &optIter{op: n, seed: seed, in: e.open(n.Input, seed)}
	case *plan.Apply:
		return &applyIter{eng: e, op: n, left: e.open(n.Left, seed)}
	case *plan.Create:
		return &createIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Merge:
		return &mergeIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.SetProps:
		return &setIter{eng: e, props: n.Items, in: e.open(n.Input, seed)}
	case *plan.SetLabels:
		return &setIter{eng: e, labels: n.Items, in: e.open(n.Input, seed)}
	case *plan.Remove:
		return &removeIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Delete:
		return &deleteIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.ShortestPath:
		return &shortIter{eng: e, op: n, in: e.open(n.Input, seed)}
	case *plan.Leaf:
		return &leafIter{eng: e, op: n, seed: seed}
	default:
		return &errIter{err: gerr.New(gerr.Internal, "unknown operator")}
	}
}

type errIter struct{ err error }

func (e *errIter) Next(context.Context) (row, bool, error) { return row{}, false, e.err }

type filterIter struct {
	eng *engine
	op  *plan.Filter
	in  iterator
}

func (f *filterIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		r, ok, err := f.in.Next(ctx)
		if !ok || err != nil {
			return r, ok, err
		}
		if f.op.Expr != nil {
			v, err := expr.Eval(f.op.Expr, f.eng.env(r))
			if err != nil {
				return row{}, false, err
			}
			b, isBool := v.BoolValue()
			if !isBool || !b {
				continue
			}
		}
		if f.op.Node != nil {
			id, ok := boundNode(r, f.op.Node.Name)
			if !ok {
				continue
			}
			n, ok := f.eng.view.Node(id)
			if !ok {
				continue
			}
			good, err := f.eng.nodeOK(n, f.op.Node, f.eng.env(r))
			if err != nil {
				return row{}, false, err
			}
			if !good {
				continue
			}
		}
		return r, true, nil
	}
}

type projectIter struct {
	eng *engine
	op  *plan.Project
	in  iterator
}

func (p *projectIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := p.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	out := blank()
	if len(p.op.Star) > 0 {
		for _, name := range p.op.Star {
			v, ok := r.val[name]
			if !ok {
				v = value.Null()
			}
			out.val[name] = v
		}
		return out, true, nil
	}
	env := p.eng.env(r)
	for _, it := range p.op.Items {
		v, err := expr.Eval(it.Expr, env)
		if err != nil {
			return row{}, false, err
		}
		name := it.As
		if name == "" {
			name = ast.Format(it.Expr)
		}
		out.val[name] = v
	}
	return out, true, nil
}

type sortIter struct {
	eng   *engine
	op    *plan.Sort
	in    iterator
	rows  []row
	keys  [][]value.Value
	order []int
	i     int
	ready bool
}

func (s *sortIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !s.ready {
		if err := s.pull(ctx); err != nil {
			return row{}, false, err
		}
		s.ready = true
	}
	if s.i >= len(s.order) {
		return row{}, false, nil
	}
	r := s.rows[s.order[s.i]]
	s.i++
	return r, true, nil
}

func (s *sortIter) pull(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, ok, err := s.in.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		env := s.eng.env(r)
		ks := make([]value.Value, len(s.op.Keys))
		n := rowBytes(r)
		for i, k := range s.op.Keys {
			v, err := expr.Eval(k.Expr, env)
			if err != nil {
				return err
			}
			ks[i] = v
			n += nbytes(v)
		}
		if err := s.eng.mem.add(n); err != nil {
			return err
		}
		s.rows = append(s.rows, r)
		s.keys = append(s.keys, ks)
	}
	idx := make([]int, len(s.rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return keyLess(s.op.Keys, s.keys[idx[i]], s.keys[idx[j]])
	})
	s.order = idx
	return nil
}

func keyLess(keys []*ast.Sort, a, b []value.Value) bool {
	for i, k := range keys {
		c := cmpNulls(a[i], b[i], k.Nulls)
		if c == 0 {
			continue
		}
		if k.Desc {
			return c > 0
		}
		return c < 0
	}
	return false
}

func cmpNulls(a, b value.Value, nulls ast.Nulls) int {
	an := a.Kind() == value.KindNull
	bn := b.Kind() == value.KindNull
	if an || bn {
		if an && bn {
			return 0
		}
		if nulls == ast.NullsLast {
			if an {
				return 1
			}
			return -1
		}
		if an {
			return -1
		}
		return 1
	}
	return value.Compare(a, b)
}

type limitIter struct {
	eng   *engine
	op    *plan.Limit
	in    iterator
	left  int64
	ready bool
}

func (l *limitIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !l.ready {
		n, err := l.eng.card(l.op.Count)
		if err != nil {
			return row{}, false, err
		}
		l.left = n
		l.ready = true
	}
	if l.left <= 0 {
		return row{}, false, nil
	}
	r, ok, err := l.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	l.left--
	return r, true, nil
}

type skipIter struct {
	eng   *engine
	op    *plan.Skip
	in    iterator
	left  int64
	ready bool
}

func (s *skipIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !s.ready {
		n, err := s.eng.card(s.op.Count)
		if err != nil {
			return row{}, false, err
		}
		s.left = n
		s.ready = true
	}
	for s.left > 0 {
		_, ok, err := s.in.Next(ctx)
		if !ok || err != nil {
			return row{}, ok, err
		}
		s.left--
	}
	return s.in.Next(ctx)
}

func (e *engine) card(x ast.Expr) (int64, error) {
	v, err := expr.Eval(x, e.env(blank()))
	if err != nil {
		return 0, err
	}
	n, ok := v.IntValue()
	if !ok || n < 0 {
		return 0, gerr.New(gerr.InvalidArgument, "SKIP and LIMIT expect a non-negative int")
	}
	return n, nil
}

type distinctIter struct {
	eng  *engine
	in   iterator
	seen []row
}

func (d *distinctIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		r, ok, err := d.in.Next(ctx)
		if !ok || err != nil {
			return r, ok, err
		}
		if d.has(r) {
			continue
		}
		if err := d.eng.mem.add(rowBytes(r)); err != nil {
			return row{}, false, err
		}
		d.seen = append(d.seen, r.clone())
		return r, true, nil
	}
}

func (d *distinctIter) has(r row) bool {
	for _, s := range d.seen {
		if sameRow(s, r) {
			return true
		}
	}
	return false
}

type unionIter struct {
	eng     *engine
	op      *plan.Union
	left    iterator
	right   iterator
	onRight bool
	seen    []row
}

func (u *unionIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		it := u.left
		if u.onRight {
			it = u.right
		}
		r, ok, err := it.Next(ctx)
		if err != nil {
			return row{}, false, err
		}
		if !ok {
			if u.onRight {
				return row{}, false, nil
			}
			u.onRight = true
			u.right = u.eng.open(u.op.Right, blank())
			continue
		}
		if u.op.All {
			return r, true, nil
		}
		if u.has(r) {
			continue
		}
		if err := u.eng.mem.add(rowBytes(r)); err != nil {
			return row{}, false, err
		}
		u.seen = append(u.seen, r.clone())
		return r, true, nil
	}
}

func (u *unionIter) has(r row) bool {
	for _, s := range u.seen {
		if sameRow(s, r) {
			return true
		}
	}
	return false
}

type unwindIter struct {
	eng    *engine
	op     *plan.Unwind
	in     iterator
	cur    row
	elems  []value.Value
	i      int
	active bool
}

func (u *unwindIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		if !u.active {
			r, ok, err := u.in.Next(ctx)
			if !ok || err != nil {
				return r, ok, err
			}
			v, err := expr.Eval(u.op.Expr, u.eng.env(r))
			if err != nil {
				return row{}, false, err
			}
			if v.Kind() == value.KindNull {
				continue
			}
			xs, ok := v.ListValue()
			if !ok {
				return row{}, false, gerr.New(gerr.InvalidArgument, "UNWIND expects a list")
			}
			u.cur = r
			u.elems = xs
			u.i = 0
			u.active = true
		}
		if u.i >= len(u.elems) {
			u.active = false
			continue
		}
		el := u.elems[u.i]
		u.i++
		out := u.cur.clone()
		out.val[u.op.Name] = el
		return out, true, nil
	}
}

type optIter struct {
	op   *plan.Optional
	seed row
	in   iterator
	saw  bool
	done bool
}

func (o *optIter) Next(ctx context.Context) (row, bool, error) {
	if o.done {
		return row{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := o.in.Next(ctx)
	if err != nil {
		return row{}, false, err
	}
	if ok {
		o.saw = true
		return r, true, nil
	}
	o.done = true
	if o.saw {
		return row{}, false, nil
	}
	out := o.seed.clone()
	for _, name := range o.op.Names {
		out.val[name] = value.Null()
	}
	return out, true, nil
}

type applyIter struct {
	eng   *engine
	op    *plan.Apply
	left  iterator
	right iterator
}

func (a *applyIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		if a.right == nil {
			left, ok, err := a.left.Next(ctx)
			if err != nil || !ok {
				return row{}, ok, err
			}
			seed := left
			if a.op.NewMatch {
				seed = left.clone()
				seed.used = map[uint64]struct{}{}
				seed.trailN = nil
				seed.trailE = nil
			}
			a.right = a.eng.open(a.op.Right, seed)
		}
		r, ok, err := a.right.Next(ctx)
		if err != nil {
			return row{}, false, err
		}
		if !ok {
			a.right = nil
			continue
		}
		return r, true, nil
	}
}

type aggItem struct {
	name  string
	expr  ast.Expr
	slots []*ast.CallExpr
	group bool
}

type aggGroup struct {
	key  []value.Value
	aggs [][]*expr.Agg
	row  row
}

type aggIter struct {
	eng    *engine
	op     *plan.Aggregate
	in     iterator
	items  []aggItem
	groups []*aggGroup
	i      int
	ready  bool
}

func (a *aggIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !a.ready {
		if err := a.pull(ctx); err != nil {
			return row{}, false, err
		}
		a.ready = true
	}
	if a.i >= len(a.groups) {
		return row{}, false, nil
	}
	g := a.groups[a.i]
	a.i++
	return a.emit(g)
}

func (a *aggIter) pull(ctx context.Context) error {
	items := make([]aggItem, len(a.op.Items))
	for i, it := range a.op.Items {
		item, err := prepareAgg(it)
		if err != nil {
			return err
		}
		items[i] = item
	}
	a.items = items
	grouped := false
	for _, it := range items {
		if it.group {
			grouped = true
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, ok, err := a.in.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		env := a.eng.env(r)
		key := make([]value.Value, 0)
		n := int64(1)
		for _, it := range items {
			if !it.group {
				continue
			}
			v, err := expr.Eval(it.expr, env)
			if err != nil {
				return err
			}
			key = append(key, v)
			n += nbytes(v)
		}
		gp := a.find(key)
		if gp == nil {
			aggs := make([][]*expr.Agg, len(items))
			for i, it := range items {
				built, err := makeAggs(it, env)
				if err != nil {
					return err
				}
				aggs[i] = built
			}
			gp = &aggGroup{key: key, aggs: aggs, row: r.clone()}
			if err := a.eng.mem.add(n + rowBytes(r)); err != nil {
				return err
			}
			a.groups = append(a.groups, gp)
		}
		for i, it := range items {
			for s, call := range it.slots {
				if err := feedAgg(gp.aggs[i][s], call, env); err != nil {
					return err
				}
				if err := a.eng.mem.add(16); err != nil {
					return err
				}
			}
		}
	}
	if len(a.groups) == 0 && !grouped {
		env := a.eng.env(blank())
		aggs := make([][]*expr.Agg, len(items))
		for i, it := range items {
			built, err := makeAggs(it, env)
			if err != nil {
				return err
			}
			aggs[i] = built
		}
		a.groups = []*aggGroup{{aggs: aggs, row: blank()}}
	}
	return nil
}

func (a *aggIter) find(key []value.Value) *aggGroup {
	for _, g := range a.groups {
		if len(g.key) != len(key) {
			continue
		}
		same := true
		for i := range key {
			if !value.Equal(g.key[i], key[i]) {
				same = false
				break
			}
		}
		if same {
			return g
		}
	}
	return nil
}

func (a *aggIter) emit(g *aggGroup) (row, bool, error) {
	env := a.eng.env(g.row)
	out := blank()
	for i, it := range a.items {
		if !it.group {
			for s, ag := range g.aggs[i] {
				v, err := ag.Value()
				if err != nil {
					return row{}, false, err
				}
				env.Set(slotName(s), v)
			}
		}
		v, err := expr.Eval(it.expr, env)
		if err != nil {
			return row{}, false, err
		}
		out.val[it.name] = v
	}
	return out, true, nil
}

func prepareAgg(it *ast.Projection) (aggItem, error) {
	name := it.As
	if name == "" {
		name = ast.Format(it.Expr)
	}
	clone, err := cloneExpr(it.Expr)
	if err != nil {
		return aggItem{}, err
	}
	var slots []*ast.CallExpr
	rewritten := ast.Rewrite(clone, func(n ast.Node) ast.Node {
		c, ok := n.(*ast.CallExpr)
		if !ok || !aggFn(strings.ToLower(c.Name)) {
			return n
		}
		slots = append(slots, c)
		return &ast.Ident{Name: slotName(len(slots) - 1)}
	})
	ex, _ := rewritten.(ast.Expr)
	return aggItem{name: name, expr: ex, slots: slots, group: len(slots) == 0}, nil
}

func makeAggs(it aggItem, env *expr.Env) ([]*expr.Agg, error) {
	out := make([]*expr.Agg, len(it.slots))
	for i, c := range it.slots {
		var extra []value.Value
		if strings.EqualFold(c.Name, "percentileDisc") && len(c.Args) > 1 {
			v, err := expr.Eval(c.Args[1], env)
			if err != nil {
				return nil, err
			}
			extra = []value.Value{v}
		}
		ag, err := expr.NewAgg(c.Name, c.Distinct, extra...)
		if err != nil {
			return nil, err
		}
		out[i] = ag
	}
	return out, nil
}

func feedAgg(ag *expr.Agg, c *ast.CallExpr, env *expr.Env) error {
	if c.Star {
		return ag.AddRow()
	}
	if len(c.Args) == 0 {
		return ag.Add(value.Null())
	}
	v, err := expr.Eval(c.Args[0], env)
	if err != nil {
		return err
	}
	return ag.Add(v)
}

func cloneExpr(e ast.Expr) (ast.Expr, error) {
	script, err := parser.Parse("RETURN " + ast.Format(e) + ";")
	if err != nil {
		return nil, err
	}
	q := script.Stmts[0].(*ast.Query)
	ret := q.Parts[0].Clauses[0].(*ast.Return)
	return ret.Body.Items[0].Expr, nil
}

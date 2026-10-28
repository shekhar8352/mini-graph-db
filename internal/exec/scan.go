package exec

import (
	"context"
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/exec/expr"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/plan"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

const (
	modeAll = iota
	modeLabel
	modeSeek
)

type scanIter struct {
	eng   *engine
	mode  int
	node  *ast.NodePat
	path  string
	seek  *plan.IndexSeek
	seed  row
	ids   []uint64
	i     int
	ready bool
}

func (s *scanIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !s.ready {
		if err := s.eng.refresh(); err != nil {
			return row{}, false, err
		}
		ids, err := s.eng.scanIDs(s.mode, s.node, s.seek, s.seed)
		if err != nil {
			return row{}, false, err
		}
		s.ids = ids
		s.ready = true
	}
	env := s.eng.env(s.seed)
	for s.i < len(s.ids) {
		id := s.ids[s.i]
		s.i++
		n, ok := s.eng.view.Node(id)
		if !ok {
			continue
		}
		good, err := s.eng.nodeOK(n, s.node, env)
		if err != nil {
			return row{}, false, err
		}
		if !good {
			continue
		}
		out := s.seed.clone()
		if s.node != nil && s.node.Name != "" {
			out.val[s.node.Name] = value.NodeRef(id)
		}
		out.trailN = []uint64{id}
		out.trailE = nil
		if s.path != "" {
			p, err := value.PathOf([]uint64{id}, nil)
			if err != nil {
				return row{}, false, err
			}
			out.val[s.path] = p
		}
		return out, true, nil
	}
	return row{}, false, nil
}

func (e *engine) scanIDs(mode int, pat *ast.NodePat, seek *plan.IndexSeek, seed row) ([]uint64, error) {
	switch mode {
	case modeSeek:
		return e.seekIDs(pat, seek, seed)
	case modeLabel:
		return e.labelIDs(pat), nil
	default:
		return e.allIDs(), nil
	}
}

func (e *engine) allIDs() []uint64 {
	ids := make([]uint64, 0, len(e.view.nodes))
	for id := range e.view.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	e.hit(int64(len(ids)))
	return ids
}

func (e *engine) labelIDs(pat *ast.NodePat) []uint64 {
	if pat == nil || len(pat.Labels) == 0 || len(pat.Labels[0]) == 0 {
		return e.allIDs()
	}
	seen := map[uint64]struct{}{}
	var ids []uint64
	for _, lab := range pat.Labels[0] {
		for _, n := range e.g.NodesByLabel(lab) {
			e.hit(1)
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			ids = append(ids, n.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (e *engine) seekIDs(pat *ast.NodePat, seek *plan.IndexSeek, seed row) ([]uint64, error) {
	if seek != nil && seek.Name != "" {
		return e.declaredSeek(seek, seed)
	}
	if pat == nil || pat.Props == nil || len(pat.Props.Entries) == 0 {
		return e.labelIDs(pat), nil
	}
	ent := pat.Props.Entries[0]
	v, err := expr.Eval(ent.Value, e.env(seed))
	if err != nil {
		return nil, err
	}
	if v.Kind() == value.KindNull || !v.Storable() {
		return nil, nil
	}
	var ids []uint64
	for _, n := range e.g.NodesByValue(ent.Key, v) {
		e.hit(1)
		ids = append(ids, n.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (e *engine) declaredSeek(seek *plan.IndexSeek, seed row) ([]uint64, error) {
	env := e.env(seed)
	var ids []uint64
	var err error
	switch seek.Op {
	case "starts":
		if seek.Prefix == nil {
			return nil, nil
		}
		v, err := expr.Eval(seek.Prefix, env)
		if err != nil {
			return nil, err
		}
		s, ok := v.StringValue()
		if !ok {
			return nil, nil
		}
		ids, err = e.g.IndexStartsWith(seek.Name, s)
	case "range":
		var lo, hi []value.Value
		loInc, hiInc := seek.LowInc, seek.HighInc
		if seek.Low != nil {
			v, err := expr.Eval(seek.Low, env)
			if err != nil {
				return nil, err
			}
			lo = []value.Value{v}
		}
		if seek.High != nil {
			v, err := expr.Eval(seek.High, env)
			if err != nil {
				return nil, err
			}
			hi = []value.Value{v}
		}
		ids, err = e.g.IndexRange(seek.Name, lo, hi, loInc, hiInc)
	default:
		sch, err := e.g.Schema()
		if err != nil {
			return nil, err
		}
		ix, ok := sch.IndexByName(seek.Name)
		if !ok {
			return e.labelIDs(seek.Node), nil
		}
		vals, complete, err := e.indexVals(ix.Props, seek.Node, env)
		if err != nil {
			return nil, err
		}
		if complete {
			ids, err = e.g.IndexEqual(seek.Name, vals)
		} else {
			ids, err = e.g.IndexPrefix(seek.Name, vals)
		}
	}
	if err != nil {
		return nil, err
	}
	e.hit(int64(len(ids)))
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (e *engine) indexVals(props []string, pat *ast.NodePat, env *expr.Env) ([]value.Value, bool, error) {
	have := map[string]ast.Expr{}
	if pat != nil && pat.Props != nil {
		for _, ent := range pat.Props.Entries {
			have[ent.Key] = ent.Value
		}
	}
	var vals []value.Value
	for _, p := range props {
		ex, ok := have[p]
		if !ok {
			return vals, false, nil
		}
		v, err := expr.Eval(ex, env)
		if err != nil {
			return nil, false, err
		}
		if v.Kind() == value.KindNull || !v.Storable() {
			return nil, false, nil
		}
		vals = append(vals, v)
	}
	return vals, true, nil
}

func (e *engine) nodeOK(n expr.NodeRec, pat *ast.NodePat, env *expr.Env) (bool, error) {
	if pat == nil {
		return true, nil
	}
	if !labelsOK(n.Labels, pat.Labels) {
		return false, nil
	}
	return e.propsOK(n.Props, pat.Props, env)
}

func labelsOK(have []string, groups [][]string) bool {
	set := map[string]struct{}{}
	for _, l := range have {
		set[l] = struct{}{}
	}
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		ok := false
		for _, l := range g {
			if _, found := set[l]; found {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (e *engine) propsOK(have map[string]value.Value, m *ast.MapLit, env *expr.Env) (bool, error) {
	if m == nil {
		return true, nil
	}
	for _, ent := range m.Entries {
		want, err := expr.Eval(ent.Value, env)
		if err != nil {
			return false, err
		}
		got, ok := have[ent.Key]
		if !ok {
			got = value.Null()
		}
		eq, err := e.predEq(env, got, want)
		if err != nil {
			return false, err
		}
		if !eq {
			return false, nil
		}
	}
	return true, nil
}

func (e *engine) predEq(env *expr.Env, a, b value.Value) (bool, error) {
	env.Set("\x00a", a)
	env.Set("\x00b", b)
	v, err := expr.Eval(&ast.Binary{Op: "=", Left: &ast.Ident{Name: "\x00a"}, Right: &ast.Ident{Name: "\x00b"}}, env)
	if err != nil {
		return false, err
	}
	bit, ok := v.BoolValue()
	return ok && bit, nil
}

type expIter struct {
	eng    *engine
	op     *plan.Expand
	in     iterator
	cur    row
	stack  []*eframe
	lo, hi int
	capped bool
	rel    *ast.RelPat
	to     *ast.NodePat
	from   string
}

type eframe struct {
	at      uint64
	nodes   []uint64
	edges   []uint64
	adj     []expr.EdgeRec
	i       int
	blocked map[uint64]struct{}
	emitted bool
}

func newExpand(e *engine, op *plan.Expand, in iterator) *expIter {
	it := &expIter{eng: e, op: op, in: in}
	if op.Hop != nil {
		if len(op.Hop.Nodes) > 0 && op.Hop.Nodes[0] != nil {
			it.from = op.Hop.Nodes[0].Name
		}
		if len(op.Hop.Nodes) > 1 {
			it.to = op.Hop.Nodes[1]
		}
		if len(op.Hop.Rels) > 0 {
			it.rel = op.Hop.Rels[0]
		}
	}
	it.lo, it.hi, it.capped = hopBounds(it.rel, e.hops)
	return it
}

func hopBounds(rel *ast.RelPat, limit int) (lo, hi int, capped bool) {
	if rel == nil || !rel.VarLen {
		return 1, 1, false
	}
	lo = int(rel.Min)
	if lo < 1 {
		lo = 1
	}
	if rel.Max < 0 || rel.Max > int64(limit) {
		return lo, limit, true
	}
	return lo, int(rel.Max), false
}

func (it *expIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		if len(it.stack) == 0 {
			r, ok, err := it.in.Next(ctx)
			if err != nil || !ok {
				return row{}, ok, err
			}
			it.cur = r
			if err := it.eng.refresh(); err != nil {
				return row{}, false, err
			}
			v, ok := r.val[it.from]
			if !ok {
				continue
			}
			id, ok := v.NodeID()
			if !ok {
				continue
			}
			it.push(id, []uint64{id}, nil, copySet(r.used))
			continue
		}
		f := it.stack[len(it.stack)-1]
		depth := len(f.edges)
		if !f.emitted && depth >= it.lo && depth <= it.hi {
			good, err := it.endOK(f)
			if err != nil {
				return row{}, false, err
			}
			f.emitted = true
			if good {
				out, err := it.emit(f)
				if err != nil {
					return row{}, false, err
				}
				return out, true, nil
			}
		}
		if depth >= it.hi || f.i >= len(f.adj) {
			it.stack = it.stack[:len(it.stack)-1]
			continue
		}
		e := f.adj[f.i]
		f.i++
		if _, used := f.blocked[e.ID]; used {
			continue
		}
		if !it.relOK(e, f.at) {
			continue
		}
		ok, err := it.relProps(e)
		if err != nil {
			return row{}, false, err
		}
		if !ok {
			continue
		}
		if it.capped && len(f.edges)+1 >= it.hi {
			return row{}, false, gerr.New(gerr.ResourceExhausted, "pattern hop limit")
		}
		next := otherEnd(e, f.at)
		nodes := append(append([]uint64{}, f.nodes...), next)
		edges := append(append([]uint64{}, f.edges...), e.ID)
		blocked := copySet(f.blocked)
		blocked[e.ID] = struct{}{}
		it.push(next, nodes, edges, blocked)
	}
}

func (it *expIter) push(at uint64, nodes, edges []uint64, blocked map[uint64]struct{}) {
	it.stack = append(it.stack, &eframe{
		at: at, nodes: nodes, edges: edges, adj: it.adj(at), blocked: blocked,
	})
}

func (it *expIter) adj(at uint64) []expr.EdgeRec {
	if it.rel == nil || it.rel.Dir == ast.DirOut {
		return it.eng.view.Out(at)
	}
	if it.rel.Dir == ast.DirIn {
		return it.eng.view.In(at)
	}
	return mergeAdj(it.eng.view.Out(at), it.eng.view.In(at))
}

func mergeAdj(a, b []expr.EdgeRec) []expr.EdgeRec {
	seen := map[uint64]struct{}{}
	out := make([]expr.EdgeRec, 0, len(a)+len(b))
	for _, e := range append(append([]expr.EdgeRec{}, a...), b...) {
		if _, ok := seen[e.ID]; ok {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	sortEdges(out)
	return out
}

func otherEnd(e expr.EdgeRec, from uint64) uint64 {
	if e.From == from {
		return e.To
	}
	return e.From
}

func (it *expIter) relOK(e expr.EdgeRec, from uint64) bool {
	rel := it.rel
	if rel == nil {
		return e.From == from
	}
	switch rel.Dir {
	case ast.DirIn:
		if e.To != from {
			return false
		}
	case ast.DirEither:
		if e.From != from && e.To != from {
			return false
		}
	default:
		if e.From != from {
			return false
		}
	}
	if len(rel.Types) == 0 {
		return true
	}
	for _, t := range rel.Types {
		if t == e.Type {
			return true
		}
	}
	return false
}

func (it *expIter) relProps(e expr.EdgeRec) (bool, error) {
	if it.rel == nil {
		return true, nil
	}
	return it.eng.propsOK(e.Props, it.rel.Props, it.eng.env(it.cur))
}

func (it *expIter) endOK(f *eframe) (bool, error) {
	if it.to == nil {
		return true, nil
	}
	id := f.nodes[len(f.nodes)-1]
	if it.to.Name != "" {
		if v, ok := it.cur.val[it.to.Name]; ok {
			have, isNode := v.NodeID()
			if !isNode || have != id {
				return false, nil
			}
		}
	}
	n, ok := it.eng.view.Node(id)
	if !ok {
		return false, nil
	}
	return it.eng.nodeOK(n, it.to, it.eng.env(it.cur))
}

func (it *expIter) emit(f *eframe) (row, error) {
	out := it.cur.clone()
	nodes, edges := f.nodes, f.edges
	if len(it.cur.trailN) > 0 && it.cur.trailN[len(it.cur.trailN)-1] == f.nodes[0] {
		nodes = append(append([]uint64{}, it.cur.trailN...), f.nodes[1:]...)
		edges = append(append([]uint64{}, it.cur.trailE...), f.edges...)
	}
	out.trailN = append([]uint64{}, nodes...)
	out.trailE = append([]uint64{}, edges...)
	for _, id := range f.edges {
		out.used[id] = struct{}{}
	}
	if it.rel != nil && it.rel.Name != "" {
		if it.rel.VarLen {
			xs := make([]value.Value, len(f.edges))
			for i, id := range f.edges {
				xs[i] = value.EdgeRef(id)
			}
			out.val[it.rel.Name] = value.List(xs...)
		} else if len(f.edges) == 1 {
			out.val[it.rel.Name] = value.EdgeRef(f.edges[0])
		}
	}
	if it.to != nil && it.to.Name != "" {
		out.val[it.to.Name] = value.NodeRef(f.nodes[len(f.nodes)-1])
	}
	if it.op.Path != "" {
		p, err := value.PathOf(nodes, edges)
		if err != nil {
			return row{}, err
		}
		out.val[it.op.Path] = p
	}
	return out, nil
}

type shortIter struct {
	eng *engine
	op  *plan.ShortestPath
	in  iterator
	buf []row
	i   int
}

func (s *shortIter) Next(ctx context.Context) (row, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return row{}, false, err
		}
		if s.i < len(s.buf) {
			r := s.buf[s.i]
			s.i++
			return r, true, nil
		}
		r, ok, err := s.in.Next(ctx)
		if err != nil || !ok {
			return row{}, ok, err
		}
		if err := s.eng.refresh(); err != nil {
			return row{}, false, err
		}
		rows, err := s.eng.shortestRows(r, s.op.Path)
		if err != nil {
			return row{}, false, err
		}
		s.buf = rows
		s.i = 0
	}
}

func (e *engine) shortestRows(r row, p *ast.Path) ([]row, error) {
	if p == nil || len(p.Nodes) < 2 || len(p.Rels) < 1 {
		return []row{r}, nil
	}
	fromV, ok1 := r.val[p.Nodes[0].Name]
	toV, ok2 := r.val[p.Nodes[1].Name]
	if !ok1 || !ok2 {
		return nil, gerr.New(gerr.InvalidArgument, "shortest path endpoint is not bound")
	}
	from, ok1 := fromV.NodeID()
	to, ok2 := toV.NodeID()
	if !ok1 || !ok2 {
		return nil, gerr.New(gerr.InvalidArgument, "shortest path endpoint is not a node")
	}
	rel := p.Rels[0]
	lo, hi, capped := hopBounds(rel, e.hops)
	paths, err := e.shortestPaths(from, to, rel, lo, hi, capped, p.Fn == ast.PathAllShortest)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		out := r.clone()
		if p.Var != "" {
			if p.Fn == ast.PathAllShortest {
				out.val[p.Var] = value.List()
			} else {
				out.val[p.Var] = value.Null()
			}
		}
		if rel.Name != "" {
			out.val[rel.Name] = value.Null()
		}
		return []row{out}, nil
	}
	rows := make([]row, len(paths))
	for i, path := range paths {
		out := r.clone()
		if p.Var != "" {
			out.val[p.Var] = path
		}
		if rel.Name != "" {
			_, edges, _ := path.PathValue()
			if rel.VarLen {
				xs := make([]value.Value, len(edges))
				for j, id := range edges {
					xs[j] = value.EdgeRef(id)
				}
				out.val[rel.Name] = value.List(xs...)
			} else if len(edges) == 1 {
				out.val[rel.Name] = value.EdgeRef(edges[0])
			} else {
				out.val[rel.Name] = value.Null()
			}
		}
		rows[i] = out
	}
	return rows, nil
}

func (e *engine) shortestPaths(from, to uint64, rel *ast.RelPat, lo, hi int, capped, all bool) ([]value.Value, error) {
	if _, ok := e.view.Node(from); !ok {
		return nil, nil
	}
	if _, ok := e.view.Node(to); !ok {
		return nil, nil
	}
	dist := map[uint64]int{from: 0}
	q := []uint64{from}
	target := -1
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		d := dist[cur]
		if target >= 0 && d >= target {
			continue
		}
		if d >= hi {
			continue
		}
		eds := e.matching(cur, rel)
		if capped && d+1 >= hi && len(eds) > 0 && target < 0 {
			return nil, gerr.New(gerr.ResourceExhausted, "pattern hop limit")
		}
		for _, ed := range eds {
			next := otherEnd(ed, cur)
			if _, ok := dist[next]; ok {
				continue
			}
			dist[next] = d + 1
			if next == to && d+1 >= lo {
				target = d + 1
				if !all {
					break
				}
			}
			q = append(q, next)
		}
	}
	if target < 0 {
		return nil, nil
	}
	var got []value.Value
	var dfs func(uint64, int, []uint64, []uint64, map[uint64]struct{})
	dfs = func(node uint64, hops int, nodes, edges []uint64, used map[uint64]struct{}) {
		if hops == target {
			if node == to {
				p, err := value.PathOf(nodes, edges)
				if err == nil {
					got = append(got, p)
				}
			}
			return
		}
		for _, ed := range e.matching(node, rel) {
			if _, seen := used[ed.ID]; seen {
				continue
			}
			next := otherEnd(ed, node)
			if dist[next] != hops+1 {
				continue
			}
			used[ed.ID] = struct{}{}
			dfs(next, hops+1, append(append([]uint64{}, nodes...), next), append(append([]uint64{}, edges...), ed.ID), used)
			delete(used, ed.ID)
		}
	}
	dfs(from, 0, []uint64{from}, nil, map[uint64]struct{}{})
	sort.Slice(got, func(i, j int) bool { return value.Compare(got[i], got[j]) < 0 })
	if !all && len(got) > 1 {
		got = got[:1]
	}
	return got, nil
}

func (e *engine) matching(at uint64, rel *ast.RelPat) []expr.EdgeRec {
	var out []expr.EdgeRec
	for _, ed := range e.adjOf(at, rel) {
		if relTypeOK(rel, ed) {
			out = append(out, ed)
		}
	}
	return out
}

func (e *engine) adjOf(at uint64, rel *ast.RelPat) []expr.EdgeRec {
	if rel == nil || rel.Dir == ast.DirOut {
		return e.view.Out(at)
	}
	if rel.Dir == ast.DirIn {
		return e.view.In(at)
	}
	return mergeAdj(e.view.Out(at), e.view.In(at))
}

func relTypeOK(rel *ast.RelPat, e expr.EdgeRec) bool {
	if rel == nil || len(rel.Types) == 0 {
		return true
	}
	for _, t := range rel.Types {
		if t == e.Type {
			return true
		}
	}
	return false
}

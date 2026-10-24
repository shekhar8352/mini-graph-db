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

type createIter struct {
	eng *engine
	op  *plan.Create
	in  iterator
}

func (c *createIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := c.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	out, err := c.eng.createPattern(r, c.op.Pattern)
	if err != nil {
		return row{}, false, err
	}
	return out, true, nil
}

type mergeIter struct {
	eng *engine
	op  *plan.Merge
	in  iterator
}

func (m *mergeIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := m.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	out, err := m.eng.mergePath(r, m.op)
	if err != nil {
		return row{}, false, err
	}
	return out, true, nil
}

type setIter struct {
	eng    *engine
	props  []*ast.SetItem
	labels []*ast.SetItem
	in     iterator
}

func (s *setIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := s.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	items := s.props
	if len(s.labels) > 0 {
		items = s.labels
	}
	if err := s.eng.applyItems(r, items); err != nil {
		return row{}, false, err
	}
	if err := s.eng.refresh(); err != nil {
		return row{}, false, err
	}
	return r, true, nil
}

type removeIter struct {
	eng *engine
	op  *plan.Remove
	in  iterator
}

func (m *removeIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := m.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	if err := m.eng.removeItems(r, m.op.Items); err != nil {
		return row{}, false, err
	}
	if err := m.eng.refresh(); err != nil {
		return row{}, false, err
	}
	return r, true, nil
}

type deleteIter struct {
	eng *engine
	op  *plan.Delete
	in  iterator
}

func (d *deleteIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	r, ok, err := d.in.Next(ctx)
	if !ok || err != nil {
		return r, ok, err
	}
	env := d.eng.env(r)
	for _, ex := range d.op.Exprs {
		v, err := expr.Eval(ex, env)
		if err != nil {
			return row{}, false, err
		}
		if err := d.eng.deleteOne(v, d.op.Detach); err != nil {
			return row{}, false, err
		}
	}
	if err := d.eng.refresh(); err != nil {
		return row{}, false, err
	}
	return r, true, nil
}

type leafIter struct {
	eng  *engine
	op   *plan.Leaf
	seed row
	rows []row
	i    int
	done bool
}

func (l *leafIter) Next(ctx context.Context) (row, bool, error) {
	if err := ctx.Err(); err != nil {
		return row{}, false, err
	}
	if !l.done {
		rows, err := l.eng.leaf(ctx, l.op, l.seed)
		if err != nil {
			return row{}, false, err
		}
		l.rows = rows
		l.done = true
	}
	if l.i >= len(l.rows) {
		return row{}, false, nil
	}
	r := l.rows[l.i]
	l.i++
	return r, true, nil
}

func (e *engine) createPattern(r row, p *ast.Pattern) (row, error) {
	out := r.clone()
	if p == nil {
		return out, nil
	}
	for _, path := range p.Paths {
		var err error
		out, err = e.createPath(out, path)
		if err != nil {
			return row{}, err
		}
	}
	if err := e.refresh(); err != nil {
		return row{}, err
	}
	return out, nil
}

func (e *engine) createPath(r row, p *ast.Path) (row, error) {
	if p == nil || len(p.Nodes) == 0 {
		return r, nil
	}
	out, id, err := e.writeNode(r, p.Nodes[0])
	if err != nil {
		return row{}, err
	}
	nodes := []uint64{id}
	var edges []uint64
	for i, rel := range p.Rels {
		var right uint64
		out, right, err = e.writeNode(out, p.Nodes[i+1])
		if err != nil {
			return row{}, err
		}
		nodes = append(nodes, right)
		var eid uint64
		out, eid, err = e.writeEdge(out, rel, nodes[i], right)
		if err != nil {
			return row{}, err
		}
		edges = append(edges, eid)
	}
	if p.Var != "" {
		pv, err := value.PathOf(nodes, edges)
		if err != nil {
			return row{}, err
		}
		out.val[p.Var] = pv
	}
	return out, nil
}

func (e *engine) writeNode(r row, n *ast.NodePat) (row, uint64, error) {
	if n == nil {
		return r, 0, gerr.New(gerr.Internal, "missing node pattern")
	}
	props, err := e.propMap(r, n.Props)
	if err != nil {
		return row{}, 0, err
	}
	labels := flatten(n.Labels)
	stored := stripNull(props)
	if n.Name != "" {
		if v, ok := r.val[n.Name]; ok {
			id, ok := v.NodeID()
			if !ok {
				return row{}, 0, gerr.New(gerr.InvalidArgument, "bound name is not a node")
			}
			node, exists := e.g.GetNode(id)
			e.hit(1)
			if !exists {
				return row{}, 0, gerr.New(gerr.InvalidArgument, "node does not exist")
			}
			if len(labels) > 0 {
				if _, err := e.g.AddLabels(id, labels); err != nil {
					return row{}, 0, err
				}
				e.sum.LabelsAdded += newLabels(node, labels)
			}
			if err := e.mergeProps(id, true, props); err != nil {
				return row{}, 0, err
			}
			return r, id, nil
		}
	}
	node, err := e.g.CreateNode(labels, stored)
	if err != nil {
		return row{}, 0, err
	}
	e.sum.NodesCreated++
	e.sum.LabelsAdded += len(labels)
	e.sum.PropertiesSet += len(stored)
	if n.Name != "" {
		r.val[n.Name] = value.NodeRef(node.ID)
	}
	return r, node.ID, nil
}

func (e *engine) writeEdge(r row, rel *ast.RelPat, left, right uint64) (row, uint64, error) {
	props, err := e.propMap(r, rel.Props)
	if err != nil {
		return row{}, 0, err
	}
	stored := stripNull(props)
	from, to := left, right
	if rel.Dir == ast.DirIn {
		from, to = right, left
	}
	typ := ""
	if len(rel.Types) > 0 {
		typ = rel.Types[0]
	}
	ed, err := e.g.CreateEdge(from, to, typ, stored)
	if err != nil {
		return row{}, 0, err
	}
	e.sum.EdgesCreated++
	e.sum.PropertiesSet += len(stored)
	if rel.Name != "" {
		r.val[rel.Name] = value.EdgeRef(ed.ID)
	}
	return r, ed.ID, nil
}

func (e *engine) mergePath(r row, op *plan.Merge) (row, error) {
	if err := e.refresh(); err != nil {
		return row{}, err
	}
	p := op.Path
	if p == nil {
		return r, nil
	}
	for _, n := range p.Nodes {
		if err := e.boundNodeOK(r, n); err != nil {
			return row{}, err
		}
	}
	hit, ok, err := e.searchMerge(r, p)
	if err != nil {
		return row{}, err
	}
	if ok {
		out := bindHit(r, p, hit.nodes, hit.edges)
		if op.OnMatch != nil {
			if err := e.applyItems(out, op.OnMatch.Items); err != nil {
				return row{}, err
			}
		}
		if err := e.refresh(); err != nil {
			return row{}, err
		}
		return out, nil
	}
	out, err := e.createPath(r.clone(), p)
	if err != nil {
		return row{}, err
	}
	if op.OnCreate != nil {
		if err := e.applyItems(out, op.OnCreate.Items); err != nil {
			return row{}, err
		}
	}
	if err := e.refresh(); err != nil {
		return row{}, err
	}
	return out, nil
}

type mergeHit struct {
	nodes []uint64
	edges []uint64
}

func (e *engine) searchMerge(r row, p *ast.Path) (mergeHit, bool, error) {
	var hits []mergeHit
	var walk func(row, int, []uint64, []uint64, map[uint64]struct{}) error
	walk = func(cur row, i int, nodes, edges []uint64, used map[uint64]struct{}) error {
		if i == len(p.Nodes) {
			hits = append(hits, mergeHit{
				nodes: append([]uint64{}, nodes...),
				edges: append([]uint64{}, edges...),
			})
			return nil
		}
		cands, err := e.mergeNodes(cur, p.Nodes[i])
		if err != nil {
			return err
		}
		for _, id := range cands {
			next := cur.clone()
			if p.Nodes[i] != nil && p.Nodes[i].Name != "" {
				next.val[p.Nodes[i].Name] = value.NodeRef(id)
			}
			nn := append(nodes, id)
			if i >= len(p.Rels) {
				if err := walk(next, i+1, nn, edges, used); err != nil {
					return err
				}
				continue
			}
			rel := p.Rels[i]
			right, err := e.mergeNodes(next, p.Nodes[i+1])
			if err != nil {
				return err
			}
			want := map[uint64]struct{}{}
			for _, rid := range right {
				want[rid] = struct{}{}
			}
			for _, ed := range e.adjOf(id, rel) {
				if _, seen := used[ed.ID]; seen || !relTypeOK(rel, ed) {
					continue
				}
				ok, err := e.propsOK(ed.Props, rel.Props, e.env(next))
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				dst := otherEnd(ed, id)
				if _, ok := want[dst]; !ok {
					continue
				}
				step := next.clone()
				if rel.Name != "" {
					step.val[rel.Name] = value.EdgeRef(ed.ID)
				}
				if p.Nodes[i+1] != nil && p.Nodes[i+1].Name != "" {
					step.val[p.Nodes[i+1].Name] = value.NodeRef(dst)
				}
				used2 := copySet(used)
				used2[ed.ID] = struct{}{}
				if err := walk(step, i+2, append(nn, dst), append(edges, ed.ID), used2); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(r, 0, nil, nil, map[uint64]struct{}{}); err != nil {
		return mergeHit{}, false, err
	}
	if len(hits) == 0 {
		return mergeHit{}, false, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		return edgeLess(hits[i].edges, hits[j].edges) ||
			(!edgeLess(hits[j].edges, hits[i].edges) && edgeLess(hits[i].nodes, hits[j].nodes))
	})
	return hits[0], true, nil
}

func edgeLess(a, b []uint64) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func (e *engine) mergeNodes(r row, n *ast.NodePat) ([]uint64, error) {
	if n != nil && n.Name != "" {
		if v, ok := r.val[n.Name]; ok {
			id, ok := v.NodeID()
			if !ok {
				return nil, gerr.New(gerr.InvalidArgument, "bound name is not a node")
			}
			return []uint64{id}, nil
		}
	}
	var ids []uint64
	env := e.env(r)
	for _, id := range e.allIDs() {
		node, ok := e.view.Node(id)
		if !ok {
			continue
		}
		good, err := e.nodeOK(node, n, env)
		if err != nil {
			return nil, err
		}
		if good {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (e *engine) boundNodeOK(r row, n *ast.NodePat) error {
	if n == nil || n.Name == "" {
		return nil
	}
	v, ok := r.val[n.Name]
	if !ok {
		return nil
	}
	id, ok := v.NodeID()
	if !ok {
		return gerr.New(gerr.InvalidArgument, "bound name is not a node")
	}
	node, ok := e.view.Node(id)
	if !ok {
		return gerr.New(gerr.InvalidArgument, "node does not exist")
	}
	good, err := e.nodeOK(node, n, e.env(r))
	if err != nil {
		return err
	}
	if !good {
		return gerr.New(gerr.InvalidArgument, "bound node does not match")
	}
	return nil
}

func bindHit(r row, p *ast.Path, nodes, edges []uint64) row {
	out := r.clone()
	for i, n := range p.Nodes {
		if n != nil && n.Name != "" {
			if _, ok := out.val[n.Name]; !ok && i < len(nodes) {
				out.val[n.Name] = value.NodeRef(nodes[i])
			}
		}
	}
	for i, rel := range p.Rels {
		if rel != nil && rel.Name != "" {
			if _, ok := out.val[rel.Name]; !ok && i < len(edges) {
				out.val[rel.Name] = value.EdgeRef(edges[i])
			}
		}
	}
	if p.Var != "" {
		if pv, err := value.PathOf(nodes, edges); err == nil {
			out.val[p.Var] = pv
		}
	}
	return out
}

func (e *engine) applyItems(r row, items []*ast.SetItem) error {
	for _, it := range items {
		if err := e.applyItem(r, it); err != nil {
			return err
		}
	}
	return e.refresh()
}

func (e *engine) applyItem(r row, it *ast.SetItem) error {
	if it == nil {
		return nil
	}
	if it.Op == ast.SetLabels {
		id, err := e.nodeID(r, it.Name)
		if err != nil {
			return err
		}
		node, _ := e.g.GetNode(id)
		e.hit(1)
		labels := flatten(it.Labels)
		if _, err = e.g.AddLabels(id, labels); err != nil {
			return err
		}
		e.sum.LabelsAdded += newLabels(node, labels)
		return nil
	}
	id, node, err := e.entity(r, it.Name)
	if err != nil {
		return err
	}
	env := e.env(r)
	switch it.Op {
	case ast.SetReplace:
		v, err := expr.Eval(it.Value, env)
		if err != nil {
			return err
		}
		if v.Kind() == value.KindNull {
			return e.replaceProps(id, node, nil)
		}
		m, ok := v.MapValue()
		if !ok {
			return gerr.New(gerr.InvalidArgument, "SET replace expects a map or null")
		}
		return e.replaceProps(id, node, stripNull(m))
	case ast.SetPlus:
		v, err := expr.Eval(it.Value, env)
		if err != nil {
			return err
		}
		m, ok := v.MapValue()
		if !ok {
			return gerr.New(gerr.InvalidArgument, "SET plus expects a map")
		}
		return e.mergeProps(id, node, m)
	default:
		v, err := expr.Eval(it.Value, env)
		if err != nil {
			return err
		}
		if v.Kind() == value.KindNull {
			return e.dropProps(id, node, []string{it.Prop})
		}
		return e.mergeProps(id, node, map[string]value.Value{it.Prop: v})
	}
}

func (e *engine) removeItems(r row, items []*ast.RemoveItem) error {
	for _, it := range items {
		if it == nil {
			continue
		}
		if it.Prop != "" {
			id, node, err := e.entity(r, it.Name)
			if err != nil {
				return err
			}
			if err := e.dropProps(id, node, []string{it.Prop}); err != nil {
				return err
			}
			continue
		}
		id, err := e.nodeID(r, it.Name)
		if err != nil {
			return err
		}
		node, _ := e.g.GetNode(id)
		e.hit(1)
		labels := flatten(it.Labels)
		if _, err := e.g.RemoveLabels(id, labels); err != nil {
			return err
		}
		e.sum.LabelsRemoved += heldLabels(node, labels)
	}
	return nil
}

func (e *engine) deleteOne(v value.Value, detach bool) error {
	if v.Kind() == value.KindNull {
		return nil
	}
	if id, ok := v.NodeID(); ok {
		_, exists := e.g.GetNode(id)
		e.hit(1)
		if !exists {
			return nil
		}
		ids, err := e.g.IncidentEdges(id)
		e.hit(1)
		if err != nil {
			return err
		}
		if len(ids) > 0 && !detach {
			return gerr.New(gerr.ConstraintViolation, "node has edges")
		}
		if err := e.g.DeleteNode(id); err != nil {
			return err
		}
		if detach {
			e.sum.EdgesDeleted += uniqueIDs(ids)
		}
		e.sum.NodesDeleted++
		return nil
	}
	if id, ok := v.EdgeID(); ok {
		_, exists := e.g.GetEdge(id)
		e.hit(1)
		if !exists {
			return nil
		}
		if err := e.g.DeleteEdge(id); err != nil {
			return err
		}
		e.sum.EdgesDeleted++
		return nil
	}
	return gerr.New(gerr.InvalidArgument, "DELETE expects a node or an edge")
}

func (e *engine) entity(r row, name string) (uint64, bool, error) {
	v, ok := r.val[name]
	if !ok {
		return 0, false, gerr.Newf(gerr.InvalidArgument, "unbound name %s", name)
	}
	if id, ok := v.NodeID(); ok {
		return id, true, nil
	}
	if id, ok := v.EdgeID(); ok {
		return id, false, nil
	}
	return 0, false, gerr.New(gerr.InvalidArgument, "SET expects a node or an edge")
}

func (e *engine) nodeID(r row, name string) (uint64, error) {
	id, node, err := e.entity(r, name)
	if err != nil {
		return 0, err
	}
	if !node {
		return 0, gerr.New(gerr.InvalidArgument, "labels expect a node")
	}
	return id, nil
}

func (e *engine) mergeProps(id uint64, node bool, props map[string]value.Value) error {
	set := map[string]value.Value{}
	var drop []string
	for k, v := range props {
		if v.Kind() == value.KindNull {
			drop = append(drop, k)
			continue
		}
		set[k] = v
	}
	if len(drop) > 0 {
		if err := e.dropProps(id, node, drop); err != nil {
			return err
		}
	}
	if len(set) == 0 {
		return nil
	}
	if node {
		if _, err := e.g.UpdateNodeValues(id, set); err != nil {
			return err
		}
	} else if _, err := e.g.UpdateEdgeValues(id, set); err != nil {
		return err
	}
	e.sum.PropertiesSet += len(set)
	return nil
}

func (e *engine) replaceProps(id uint64, node bool, props map[string]value.Value) error {
	if node {
		if _, err := e.g.ReplaceNodeProps(id, props); err != nil {
			return err
		}
	} else if _, err := e.g.ReplaceEdgeProps(id, props); err != nil {
		return err
	}
	e.sum.PropertiesSet += len(props)
	return nil
}

func (e *engine) dropProps(id uint64, node bool, names []string) error {
	if node {
		_, err := e.g.DeleteNodeProps(id, names)
		return err
	}
	_, err := e.g.DeleteEdgeProps(id, names)
	return err
}

func (e *engine) propMap(r row, m *ast.MapLit) (map[string]value.Value, error) {
	if m == nil {
		return nil, nil
	}
	env := e.env(r)
	out := map[string]value.Value{}
	for _, ent := range m.Entries {
		v, err := expr.Eval(ent.Value, env)
		if err != nil {
			return nil, err
		}
		out[ent.Key] = v
	}
	return out, nil
}

func (e *engine) leaf(ctx context.Context, op *plan.Leaf, seed row) ([]row, error) {
	switch op.Name {
	case "Begin":
		return nil, e.g.Begin(op.Detail == "READ ONLY")
	case "Commit":
		return nil, e.g.Commit()
	case "Rollback":
		return nil, e.g.Rollback()
	case "Vacuum":
		_, err := e.g.Vacuum(ctx)
		return nil, err
	case "Call":
		return e.callRows(op.Detail, seed)
	default:
		return nil, nil
	}
}

func (e *engine) callRows(detail string, seed row) ([]row, error) {
	if err := e.refresh(); err != nil {
		return nil, err
	}
	var names []string
	var col string
	switch detail {
	case "db.labels()":
		col = "label"
		names = append([]string(nil), e.g.Stats().Labels...)
	case "db.edgeTypes()":
		col = "edgeType"
		seen := map[string]struct{}{}
		for _, ed := range e.g.AllEdges() {
			seen[ed.Label] = struct{}{}
		}
		for name := range seen {
			names = append(names, name)
		}
	case "db.propertyKeys()":
		col = "propertyKey"
		seen := map[string]struct{}{}
		for _, n := range e.g.AllNodes() {
			for k := range n.Properties() {
				seen[k] = struct{}{}
			}
		}
		for _, ed := range e.g.AllEdges() {
			for k := range ed.Properties() {
				seen[k] = struct{}{}
			}
		}
		for name := range seen {
			names = append(names, name)
		}
	default:
		return nil, gerr.New(gerr.InvalidArgument, "unknown procedure")
	}
	sort.Strings(names)
	rows := make([]row, len(names))
	for i, name := range names {
		out := seed.clone()
		out.val[col] = value.String(name)
		rows[i] = out
	}
	return rows, nil
}

type labelSet interface {
	HasLabel(string) bool
}

func newLabels(n labelSet, labels []string) int {
	added := 0
	for _, l := range labels {
		if n != nil && n.HasLabel(l) {
			continue
		}
		added++
	}
	return added
}

func heldLabels(n labelSet, labels []string) int {
	held := 0
	for _, l := range labels {
		if n != nil && n.HasLabel(l) {
			held++
		}
	}
	return held
}

func uniqueIDs(ids []uint64) int {
	seen := map[uint64]struct{}{}
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}

func flatten(groups [][]string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, g := range groups {
		for _, l := range g {
			if _, ok := seen[l]; ok {
				continue
			}
			seen[l] = struct{}{}
			out = append(out, l)
		}
	}
	return out
}

func stripNull(props map[string]value.Value) map[string]value.Value {
	if len(props) == 0 {
		return nil
	}
	out := map[string]value.Value{}
	for k, v := range props {
		if v.Kind() == value.KindNull {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

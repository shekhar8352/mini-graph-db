package plan

import (
	"github.com/shekhar8352/mini-graph-db/internal/catalog"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func optimize(op Op, sch *catalog.Schema) Op {
	op = rewrite(op, func(cur Op) Op {
		return pushFilter(cur, sch)
	})
	op = rewrite(op, func(cur Op) Op {
		return dropSortIfIndex(cur, sch)
	})
	op = rewrite(op, pushLimit)
	return op
}

func rewrite(op Op, fn func(Op) Op) Op {
	if op == nil {
		return nil
	}
	switch n := op.(type) {
	case *Expand:
		n.Input = rewrite(n.Input, fn)
	case *Filter:
		n.Input = rewrite(n.Input, fn)
	case *Project:
		n.Input = rewrite(n.Input, fn)
	case *Aggregate:
		n.Input = rewrite(n.Input, fn)
	case *Sort:
		n.Input = rewrite(n.Input, fn)
	case *Limit:
		n.Input = rewrite(n.Input, fn)
	case *Skip:
		n.Input = rewrite(n.Input, fn)
	case *Distinct:
		n.Input = rewrite(n.Input, fn)
	case *Union:
		n.Left = rewrite(n.Left, fn)
		n.Right = rewrite(n.Right, fn)
	case *Unwind:
		n.Input = rewrite(n.Input, fn)
	case *Optional:
		n.Input = rewrite(n.Input, fn)
	case *Apply:
		n.Left = rewrite(n.Left, fn)
		n.Right = rewrite(n.Right, fn)
	case *Create:
		n.Input = rewrite(n.Input, fn)
	case *Merge:
		n.Input = rewrite(n.Input, fn)
	case *SetProps:
		n.Input = rewrite(n.Input, fn)
	case *SetLabels:
		n.Input = rewrite(n.Input, fn)
	case *Remove:
		n.Input = rewrite(n.Input, fn)
	case *Delete:
		n.Input = rewrite(n.Input, fn)
	case *ShortestPath:
		n.Input = rewrite(n.Input, fn)
	}
	return fn(op)
}

func pushLimit(op Op) Op {
	lim, ok := op.(*Limit)
	if !ok {
		return op
	}
	proj, ok := lim.Input.(*Project)
	if !ok || proj.Input == nil {
		return op
	}
	lim.Input = proj.Input
	proj.Input = lim
	return proj
}

func dropSortIfIndex(op Op, sch *catalog.Schema) Op {
	srt, ok := op.(*Sort)
	if !ok || len(srt.Keys) == 0 || srt.Keys[0].Desc {
		return op
	}
	seek, proj, scan := indexUnder(srt.Input)
	if seek == nil && scan != nil {
		if ix, node, path, ok := indexForSort(sch, srt.Keys, scan, proj); ok {
			seek = &IndexSeek{Node: node, Path: path, Name: ix.Name, Op: "=", Prop: ix.Props[0]}
			srt.Input = replaceScan(srt.Input, scan, seek)
		}
	}
	if seek == nil {
		return op
	}
	if !orderMatches(srt.Keys, seek, proj) {
		return op
	}
	return srt.Input
}

func indexUnder(op Op) (*IndexSeek, *Project, Op) {
	switch n := op.(type) {
	case *IndexSeek:
		return n, nil, n
	case *LabelScan:
		return nil, nil, n
	case *Project:
		s, _, leaf := indexUnder(n.Input)
		return s, n, leaf
	}
	return nil, nil, nil
}

func indexForSort(sch *catalog.Schema, keys []*ast.Sort, scan Op, proj *Project) (catalog.Index, *ast.NodePat, string, bool) {
	node := scanNode(scan)
	label := scanLabel(scan)
	prop := sortProp(keys[0].Expr, node, proj)
	if prop == "" || label == "" || sch == nil {
		return catalog.Index{}, nil, "", false
	}
	for _, ix := range sch.OnlineIndexes(catalog.NodeEntity, label) {
		if len(ix.Props) > 0 && ix.Props[0] == prop {
			return ix, node, scanPath(scan), true
		}
	}
	return catalog.Index{}, nil, "", false
}

func sortProp(e ast.Expr, node *ast.NodePat, proj *Project) string {
	if p, ok := e.(*ast.Property); ok {
		if node != nil {
			if id, ok := p.X.(*ast.Ident); ok && (node.Name == "" || id.Name == node.Name) {
				return p.Name
			}
		} else {
			return p.Name
		}
	}
	if id, ok := e.(*ast.Ident); ok && proj != nil {
		for _, it := range proj.Items {
			if it.As == id.Name || ast.Format(it.Expr) == id.Name {
				if p, ok := it.Expr.(*ast.Property); ok {
					return p.Name
				}
			}
		}
	}
	return ""
}

func orderMatches(keys []*ast.Sort, seek *IndexSeek, proj *Project) bool {
	if len(keys) == 0 || keys[0].Desc {
		return false
	}
	prop := seekProp(seek)
	if prop == "" {
		return false
	}
	e := keys[0].Expr
	if p, ok := e.(*ast.Property); ok && p.Name == prop {
		return true
	}
	if id, ok := e.(*ast.Ident); ok && proj != nil {
		for _, it := range proj.Items {
			if it.As == id.Name {
				if p, ok := it.Expr.(*ast.Property); ok && p.Name == prop {
					return true
				}
			}
			if ast.Format(it.Expr) == id.Name {
				if p, ok := it.Expr.(*ast.Property); ok && p.Name == prop {
					return true
				}
			}
		}
	}
	return false
}

func seekProp(seek *IndexSeek) string {
	if seek.Prop != "" {
		return seek.Prop
	}
	if seek.Node != nil && seek.Node.Props != nil && len(seek.Node.Props.Entries) > 0 {
		return seek.Node.Props.Entries[0].Key
	}
	return ""
}

func pushFilter(op Op, sch *catalog.Schema) Op {
	f, ok := op.(*Filter)
	if !ok || f.Expr == nil || sch == nil {
		return op
	}
	scanVar, leaf := startScan(f.Input)
	if scanVar == "" || leaf == nil {
		return op
	}
	eq, rng, rest := splitPreds(f.Expr, scanVar)
	label := scanLabel(leaf)
	ix, ok := pickIndex(sch, label, eq, rng.prop, rng.low, rng.high, rng.starts)
	if !ok {
		return op
	}
	seek := &IndexSeek{Node: scanNode(leaf), Path: scanPath(leaf), Name: ix.Name}
	switch {
	case rng.starts != nil:
		seek.Op = "starts"
		seek.Prop = rng.prop
		seek.Prefix = rng.starts
	case rng.low != nil || rng.high != nil:
		seek.Op = "range"
		seek.Prop = rng.prop
		seek.Low, seek.High = rng.low, rng.high
		seek.LowInc, seek.HighInc = rng.lowInc, rng.highInc
	default:
		seek.Op = "="
	}
	replaced := replaceScan(f.Input, leaf, seek)
	if rest == nil {
		return replaced
	}
	return &Filter{Input: replaced, Expr: rest}
}

type rangePred struct {
	prop            string
	low, high       ast.Expr
	lowInc, highInc bool
	starts          ast.Expr
}

func splitPreds(e ast.Expr, self string) (map[string]ast.Expr, rangePred, ast.Expr) {
	eq := map[string]ast.Expr{}
	var rng rangePred
	var leftover []ast.Expr
	for _, p := range flattenAnd(e) {
		used := false
		b, ok := p.(*ast.Binary)
		if ok {
			prop, val, op, ok := sarg(b, self)
			if ok {
				switch op {
				case "=":
					if _, have := eq[prop]; !have {
						eq[prop] = val
						used = true
					}
				case "STARTS WITH":
					if rng.starts == nil && rng.prop == "" || rng.prop == prop {
						rng.prop = prop
						rng.starts = val
						used = true
					}
				case "<", "<=":
					if rng.starts == nil && (rng.prop == "" || rng.prop == prop) {
						rng.prop = prop
						rng.high = val
						rng.highInc = op == "<="
						used = true
					}
				case ">", ">=":
					if rng.starts == nil && (rng.prop == "" || rng.prop == prop) {
						rng.prop = prop
						rng.low = val
						rng.lowInc = op == ">="
						used = true
					}
				}
			}
		}
		if !used {
			leftover = append(leftover, p)
		}
	}
	return eq, rng, joinAnd(leftover)
}

func sarg(b *ast.Binary, self string) (prop string, val ast.Expr, op string, ok bool) {
	if prop, ok = propertyOf(b.Left, self); ok && boundExpr(b.Right, self) {
		return prop, b.Right, b.Op, true
	}
	if prop, ok = propertyOf(b.Right, self); ok && boundExpr(b.Left, self) {
		return prop, b.Left, swapOp(b.Op), true
	}
	return "", nil, "", false
}

func swapOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op
	}
}

func propertyOf(e ast.Expr, self string) (string, bool) {
	p, ok := e.(*ast.Property)
	if !ok {
		return "", false
	}
	id, ok := p.X.(*ast.Ident)
	if !ok || id.Name != self {
		return "", false
	}
	return p.Name, true
}

func boundExpr(e ast.Expr, self string) bool {
	ok := true
	ast.Walk(&identBan{self: self, bad: &ok}, e)
	return ok
}

type identBan struct {
	self string
	bad  *bool
}

func (v *identBan) Visit(n ast.Node) ast.Visitor {
	if n == nil || !*v.bad {
		return nil
	}
	switch x := n.(type) {
	case *ast.Ident:
		if x.Name == v.self {
			*v.bad = false
			return nil
		}
	case *ast.Property:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == v.self {
			*v.bad = false
			return nil
		}
	}
	return v
}

func flattenAnd(e ast.Expr) []ast.Expr {
	b, ok := e.(*ast.Binary)
	if !ok || b.Op != "AND" {
		return []ast.Expr{e}
	}
	return append(flattenAnd(b.Left), flattenAnd(b.Right)...)
}

func joinAnd(es []ast.Expr) ast.Expr {
	if len(es) == 0 {
		return nil
	}
	cur := es[0]
	for i := 1; i < len(es); i++ {
		cur = &ast.Binary{Op: "AND", Left: cur, Right: es[i]}
	}
	return cur
}

func pickIndex(sch *catalog.Schema, label string, eq map[string]ast.Expr, rangeProp string, low, high ast.Expr, starts ast.Expr) (catalog.Index, bool) {
	if sch == nil || label == "" {
		return catalog.Index{}, false
	}
	best, ok := catalog.Index{}, false
	bestN := 0
	for _, ix := range sch.OnlineIndexes(catalog.NodeEntity, label) {
		n := 0
		for _, p := range ix.Props {
			if _, have := eq[p]; have {
				n++
				continue
			}
			if rangeProp == p && (low != nil || high != nil || starts != nil) {
				n++
			}
			break
		}
		if n == 0 {
			continue
		}
		if !ok || n > bestN || n == bestN && ix.Unique && !best.Unique || n == bestN && ix.Unique == best.Unique && ix.Name < best.Name {
			best, ok, bestN = ix, true, n
		}
	}
	return best, ok
}

func startScan(op Op) (string, Op) {
	switch n := op.(type) {
	case *IndexSeek:
		return nodeName(n.Node), n
	case *LabelScan:
		return nodeName(n.Node), n
	case *NodeScan:
		return nodeName(n.Node), n
	case *Expand:
		return startScan(n.Input)
	case *Filter:
		return startScan(n.Input)
	}
	return "", nil
}

func nodeName(n *ast.NodePat) string {
	if n == nil {
		return ""
	}
	return n.Name
}

func scanLabel(op Op) string {
	n := scanNode(op)
	if n == nil || len(n.Labels) == 0 || len(n.Labels[0]) == 0 {
		return ""
	}
	return n.Labels[0][0]
}

func scanNode(op Op) *ast.NodePat {
	switch n := op.(type) {
	case *IndexSeek:
		return n.Node
	case *LabelScan:
		return n.Node
	case *NodeScan:
		return n.Node
	}
	return nil
}

func scanPath(op Op) string {
	switch n := op.(type) {
	case *IndexSeek:
		return n.Path
	case *LabelScan:
		return n.Path
	case *NodeScan:
		return n.Path
	}
	return ""
}

func replaceScan(op, old, neu Op) Op {
	if op == old {
		return neu
	}
	switch n := op.(type) {
	case *Expand:
		n.Input = replaceScan(n.Input, old, neu)
	case *Filter:
		n.Input = replaceScan(n.Input, old, neu)
	case *Project:
		n.Input = replaceScan(n.Input, old, neu)
	case *Sort:
		n.Input = replaceScan(n.Input, old, neu)
	}
	return op
}

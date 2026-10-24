package exec

import (
	"context"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/lang/plan"
)

func (e *engine) open(op plan.Op, seed row) iterator {
	it := e.openPlan(op, seed)
	if !e.profile || op == nil {
		return it
	}
	st := e.seen[op]
	if st == nil {
		return it
	}
	return &profIter{eng: e, st: st, in: it}
}

func (e *engine) indexPlan(op plan.Op, depth int) {
	if op == nil {
		return
	}
	if _, ok := e.seen[op]; ok {
		return
	}
	if e.seen == nil {
		e.seen = map[plan.Op]*Operator{}
	}
	st := &Operator{Text: plan.Title(op), Depth: depth}
	e.seen[op] = st
	e.ops = append(e.ops, st)
	for _, kid := range plan.Children(op) {
		e.indexPlan(kid, depth+1)
	}
}

func (e *engine) hit(n int64) {
	if e == nil || e.cur == nil || n <= 0 {
		return
	}
	e.cur.DBHits += n
}

type profIter struct {
	eng *engine
	st  *Operator
	in  iterator
}

func (p *profIter) Next(ctx context.Context) (row, bool, error) {
	prev := p.eng.cur
	p.eng.cur = p.st
	start := time.Now()
	r, ok, err := p.in.Next(ctx)
	p.st.Time += time.Since(start)
	p.eng.cur = prev
	if err == nil && ok {
		p.st.Rows++
	}
	return r, ok, err
}

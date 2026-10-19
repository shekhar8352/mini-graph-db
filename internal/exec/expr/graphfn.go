package expr

import (
	"sort"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func fnGraph(name string, args []value.Value, env *Env) (value.Value, error) {
	switch name {
	case "id":
		return fnID(args[0])
	case "labels":
		return fnLabels(args[0], env)
	case "type":
		return fnEdgeType(args[0], env)
	case "startnode":
		return fnEnd(args[0], env, true)
	case "endnode":
		return fnEnd(args[0], env, false)
	case "properties":
		return fnProps(args[0], env)
	case "degree":
		return fnDegree(args, env)
	case "neighbors":
		return fnNeighbors(args[0], args[1], env)
	case "shortestpath":
		return fnShortest(args[0], args[1], env, false)
	case "allshortestpaths":
		return fnShortest(args[0], args[1], env, true)
	case "length":
		return fnLength(args[0])
	case "nodes":
		return fnPathNodes(args[0], true)
	default:
		return fnPathNodes(args[0], false)
	}
}

func fnID(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindNode:
		id, _ := v.NodeID()
		return fitID(id)
	case value.KindEdge:
		id, _ := v.EdgeID()
		return fitID(id)
	default:
		return value.Null(), inv("id expects a node or an edge")
	}
}

func fnLabels(v value.Value, env *Env) (value.Value, error) {
	id, ok := v.NodeID()
	if !ok {
		return value.Null(), inv("labels expects a node")
	}
	n, err := needNode(env, id)
	if err != nil {
		return value.Null(), err
	}
	return stringList(n.Labels), nil
}

func fnEdgeType(v value.Value, env *Env) (value.Value, error) {
	id, ok := v.EdgeID()
	if !ok {
		return value.Null(), inv("type expects an edge")
	}
	e, err := needEdge(env, id)
	if err != nil {
		return value.Null(), err
	}
	return value.String(e.Type), nil
}

func fnEnd(v value.Value, env *Env, start bool) (value.Value, error) {
	id, ok := v.EdgeID()
	if !ok {
		return value.Null(), inv("endpoint expects an edge")
	}
	e, err := needEdge(env, id)
	if err != nil {
		return value.Null(), err
	}
	if start {
		return value.NodeRef(e.From), nil
	}
	return value.NodeRef(e.To), nil
}

func fnProps(v value.Value, env *Env) (value.Value, error) {
	switch v.Kind() {
	case value.KindMap:
		return v, nil
	case value.KindNode:
		id, _ := v.NodeID()
		n, err := needNode(env, id)
		if err != nil {
			return value.Null(), err
		}
		return value.Map(n.Props), nil
	case value.KindEdge:
		id, _ := v.EdgeID()
		e, err := needEdge(env, id)
		if err != nil {
			return value.Null(), err
		}
		return value.Map(e.Props), nil
	default:
		return value.Null(), inv("properties expects a node, edge, or map")
	}
}

func fnDegree(args []value.Value, env *Env) (value.Value, error) {
	id, ok := args[0].NodeID()
	if !ok {
		return value.Null(), inv("degree expects a node")
	}
	dir := "out"
	if len(args) == 2 {
		s, ok := args[1].StringValue()
		if !ok {
			return value.Null(), inv("degree direction expects a string")
		}
		dir = s
	}
	if _, err := needNode(env, id); err != nil {
		return value.Null(), err
	}
	var n int
	switch dir {
	case "out":
		n = len(env.Graph.Out(id))
	case "in":
		n = len(env.Graph.In(id))
	case "both":
		seen := map[uint64]struct{}{}
		for _, e := range env.Graph.Out(id) {
			seen[e.ID] = struct{}{}
		}
		for _, e := range env.Graph.In(id) {
			seen[e.ID] = struct{}{}
		}
		n = len(seen)
	default:
		return value.Null(), inv("degree direction is out, in, or both")
	}
	return value.Int(int64(n)), nil
}

func fnNeighbors(node, depth value.Value, env *Env) (value.Value, error) {
	id, ok := node.NodeID()
	if !ok {
		return value.Null(), inv("neighbors expects a node")
	}
	d, ok := depth.IntValue()
	if !ok || d < 1 {
		return value.Null(), inv("neighbors depth is below 1")
	}
	if _, err := needNode(env, id); err != nil {
		return value.Null(), err
	}
	seen := map[uint64]bool{id: true}
	frontier := []uint64{id}
	var out []value.Value
	for step := int64(0); step < d && len(frontier) > 0; step++ {
		layer := map[uint64]struct{}{}
		for _, cur := range frontier {
			for _, e := range env.Graph.Out(cur) {
				if seen[e.To] {
					continue
				}
				layer[e.To] = struct{}{}
			}
		}
		next := make([]uint64, 0, len(layer))
		for nid := range layer {
			next = append(next, nid)
		}
		sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })
		frontier = frontier[:0]
		for _, nid := range next {
			seen[nid] = true
			frontier = append(frontier, nid)
			out = append(out, value.NodeRef(nid))
		}
	}
	return value.List(out...), nil
}

func fnLength(v value.Value) (value.Value, error) {
	_, edges, ok := v.PathValue()
	if !ok {
		return value.Null(), inv("length expects a path")
	}
	return value.Int(int64(len(edges))), nil
}

func fnPathNodes(v value.Value, nodes bool) (value.Value, error) {
	ns, es, ok := v.PathValue()
	if !ok {
		return value.Null(), inv("path function expects a path")
	}
	if nodes {
		out := make([]value.Value, len(ns))
		for i, id := range ns {
			out[i] = value.NodeRef(id)
		}
		return value.List(out...), nil
	}
	out := make([]value.Value, len(es))
	for i, id := range es {
		out[i] = value.EdgeRef(id)
	}
	return value.List(out...), nil
}

type walk struct {
	node  uint64
	nodes []uint64
	edges []uint64
}

func fnShortest(a, b value.Value, env *Env, all bool) (value.Value, error) {
	from, ok1 := a.NodeID()
	to, ok2 := b.NodeID()
	if !ok1 || !ok2 {
		return value.Null(), inv("shortestPath expects nodes")
	}
	if env.Graph == nil {
		return value.Null(), inv("no graph")
	}
	if _, ok := env.Graph.Node(from); !ok {
		return missPath(all)
	}
	if _, ok := env.Graph.Node(to); !ok {
		return missPath(all)
	}
	if from == to {
		p, err := value.PathOf([]uint64{from}, nil)
		if err != nil {
			return value.Null(), err
		}
		if all {
			return value.List(p), nil
		}
		return p, nil
	}
	if all {
		return allShortest(env.Graph, from, to)
	}
	return oneShortest(env.Graph, from, to)
}

func missPath(all bool) (value.Value, error) {
	if all {
		return value.List(), nil
	}
	return value.Null(), nil
}

func oneShortest(g Graph, from, to uint64) (value.Value, error) {
	seen := map[uint64]bool{from: true}
	q := []walk{{node: from, nodes: []uint64{from}}}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, e := range g.Out(cur.node) {
			if seen[e.To] {
				continue
			}
			nodes := append(append([]uint64(nil), cur.nodes...), e.To)
			edges := append(append([]uint64(nil), cur.edges...), e.ID)
			if e.To == to {
				return value.PathOf(nodes, edges)
			}
			seen[e.To] = true
			q = append(q, walk{node: e.To, nodes: nodes, edges: edges})
		}
	}
	return value.Null(), nil
}

func allShortest(g Graph, from, to uint64) (value.Value, error) {
	dist := map[uint64]int{from: 0}
	q := []uint64{from}
	target := -1
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if target >= 0 && dist[cur] >= target {
			continue
		}
		for _, e := range g.Out(cur) {
			if _, ok := dist[e.To]; ok {
				continue
			}
			dist[e.To] = dist[cur] + 1
			if e.To == to {
				target = dist[e.To]
			}
			q = append(q, e.To)
		}
	}
	if target < 0 {
		return value.List(), nil
	}
	var got []value.Value
	var dfs func(uint64, int, []uint64, []uint64)
	dfs = func(node uint64, hops int, nodes, edges []uint64) {
		if hops == target {
			if node == to {
				p, err := value.PathOf(nodes, edges)
				if err == nil {
					got = append(got, p)
				}
			}
			return
		}
		for _, e := range g.Out(node) {
			if dist[e.To] != hops+1 {
				continue
			}
			dfs(e.To, hops+1, append(append([]uint64(nil), nodes...), e.To), append(append([]uint64(nil), edges...), e.ID))
		}
	}
	dfs(from, 0, []uint64{from}, nil)
	sort.Slice(got, func(i, j int) bool { return value.Compare(got[i], got[j]) < 0 })
	return value.List(got...), nil
}

func needNode(env *Env, id uint64) (NodeRec, error) {
	if env.Graph == nil {
		return NodeRec{}, inv("no graph")
	}
	n, ok := env.Graph.Node(id)
	if !ok {
		return NodeRec{}, inv("node does not exist")
	}
	return n, nil
}

func needEdge(env *Env, id uint64) (EdgeRec, error) {
	if env.Graph == nil {
		return EdgeRec{}, inv("no graph")
	}
	e, ok := env.Graph.Edge(id)
	if !ok {
		return EdgeRec{}, inv("edge does not exist")
	}
	return e, nil
}

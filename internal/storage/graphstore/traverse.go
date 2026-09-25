package graphstore

import "github.com/shekhar8352/mini-graph-db/internal/storage"

// Neighbor is a node reached during a bounded traversal, with its hop distance.
type Neighbor struct {
	Node  Node
	Depth int
}

// Neighbors returns nodes reachable from start along outgoing edges, up to
// depth inclusive. The start node is omitted. Depth must be at least 1.
func Neighbors(tx storage.Tx, start uint64, depth int) ([]Neighbor, error) {
	if depth < 1 {
		return nil, invalidf("depth must be >= 1")
	}
	if _, err := GetNode(tx, start); err != nil {
		return nil, err
	}
	type item struct {
		id    uint64
		depth int
	}
	seen := map[uint64]struct{}{start: {}}
	queue := []item{{id: start, depth: 0}}
	var out []Neighbor
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= depth {
			continue
		}
		tos, err := outgoing(tx, cur.id)
		if err != nil {
			return nil, err
		}
		for _, to := range tos {
			if _, ok := seen[to]; ok {
				continue
			}
			n, err := GetNode(tx, to)
			if err != nil {
				if IsNotFound(err) {
					continue
				}
				return nil, err
			}
			seen[to] = struct{}{}
			next := cur.depth + 1
			out = append(out, Neighbor{Node: n, Depth: next})
			queue = append(queue, item{id: to, depth: next})
		}
	}
	return out, nil
}

// DFS returns nodes in depth-first preorder along outgoing edges, up to depth.
// The start node is omitted.
func DFS(tx storage.Tx, start uint64, depth int) ([]Neighbor, error) {
	if depth < 1 {
		return nil, invalidf("depth must be >= 1")
	}
	if _, err := GetNode(tx, start); err != nil {
		return nil, err
	}
	seen := map[uint64]struct{}{start: {}}
	var out []Neighbor
	var walk func(id uint64, d int) error
	walk = func(id uint64, d int) error {
		if d >= depth {
			return nil
		}
		tos, err := outgoing(tx, id)
		if err != nil {
			return err
		}
		for _, to := range tos {
			if _, ok := seen[to]; ok {
				continue
			}
			n, err := GetNode(tx, to)
			if err != nil {
				if IsNotFound(err) {
					continue
				}
				return err
			}
			seen[to] = struct{}{}
			next := d + 1
			out = append(out, Neighbor{Node: n, Depth: next})
			if err := walk(to, next); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(start, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// ShortestPath returns the unweighted shortest path from src to dst, inclusive.
// The slice is empty when no path exists.
func ShortestPath(tx storage.Tx, src, dst uint64) ([]Node, error) {
	srcNode, err := GetNode(tx, src)
	if err != nil {
		return nil, err
	}
	if _, err := GetNode(tx, dst); err != nil {
		return nil, err
	}
	if src == dst {
		return []Node{srcNode}, nil
	}
	parent := map[uint64]uint64{}
	seen := map[uint64]struct{}{src: {}}
	queue := []uint64{src}
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		tos, err := outgoing(tx, cur)
		if err != nil {
			return nil, err
		}
		for _, to := range tos {
			if _, ok := seen[to]; ok {
				continue
			}
			seen[to] = struct{}{}
			parent[to] = cur
			if to == dst {
				found = true
				break
			}
			queue = append(queue, to)
		}
	}
	if !found {
		return []Node{}, nil
	}
	var ids []uint64
	for at := dst; ; at = parent[at] {
		ids = append(ids, at)
		if at == src {
			break
		}
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	path := make([]Node, 0, len(ids))
	for _, id := range ids {
		if id == src {
			path = append(path, srcNode)
			continue
		}
		n, err := GetNode(tx, id)
		if err != nil {
			return nil, err
		}
		path = append(path, n)
	}
	return path, nil
}

func outgoing(tx storage.Tx, from uint64) ([]uint64, error) {
	keys, err := scanPrefix(tx, storage.KSOut, idKey(from))
	if err != nil {
		return nil, err
	}
	var tos []uint64
	for _, k := range keys {
		_, _, to, _, ok := parseAdj(k)
		if ok {
			tos = append(tos, to)
		}
	}
	return tos, nil
}

package query

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/persist"
)

// Result is the outcome of executing a statement.
type Result struct {
	Kind      string // message, nodes, neighbors, path, stats, exit, help
	Message   string
	Nodes     []graph.Node
	Neighbors []graph.Neighbor
	Path      []graph.Node
	Edges     []graph.Edge
	Stats     graph.Stats
	Mutating  bool
	Exit      bool
}

// Executor runs parsed statements against a graph, optionally logging mutations.
type Executor struct {
	G   *graph.Graph
	WAL *persist.WAL
}

// NewExecutor binds a graph and optional WAL.
func NewExecutor(g *graph.Graph, wal *persist.WAL) *Executor {
	return &Executor{G: g, WAL: wal}
}

// ExecString parses and runs a single line. When replay is true, mutations are
// not written back to the WAL (used when restoring from the log).
func (e *Executor) ExecString(line string, replay bool) (Result, error) {
	stmt, err := Parse(line)
	if err != nil {
		return Result{}, err
	}
	res, err := e.Exec(stmt)
	if err != nil {
		return Result{}, err
	}
	if res.Mutating && !replay {
		if err := e.WAL.Append(strings.TrimSpace(line)); err != nil {
			return Result{}, fmt.Errorf("wal append: %w", err)
		}
	}
	return res, nil
}

// Exec runs a parsed statement.
func (e *Executor) Exec(stmt Stmt) (Result, error) {
	switch s := stmt.(type) {
	case CreateNodeStmt:
		n := e.G.AddNode(s.Label, s.Props)
		return Result{
			Kind:     "message",
			Message:  fmt.Sprintf("created node %d label=%s", n.ID, n.Label),
			Nodes:    []graph.Node{n},
			Mutating: true,
		}, nil
	case CreateEdgeStmt:
		ed, err := e.G.AddEdge(s.From, s.To, s.Label, s.Props)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Kind:     "message",
			Message:  fmt.Sprintf("created edge %d %d -%s-> %d", ed.ID, ed.From, ed.Label, ed.To),
			Edges:    []graph.Edge{ed},
			Mutating: true,
		}, nil
	case UpdateNodeStmt:
		n, err := e.G.UpdateNode(s.ID, s.Props)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: "nodes", Nodes: []graph.Node{n}, Message: fmt.Sprintf("updated node %d", n.ID), Mutating: true}, nil
	case UpdateEdgeStmt:
		ed, err := e.G.UpdateEdge(s.ID, s.Props)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: "edges", Message: fmt.Sprintf("updated edge %d", ed.ID), Edges: []graph.Edge{ed}, Mutating: true}, nil
	case MatchStmt:
		nodes := e.G.NodesByLabel(s.Label)
		if s.Where != nil && s.Where.Op == "=" {
			idx := e.G.NodesByPropEq(s.Where.Key, s.Where.Value)
			byID := map[uint64]graph.Node{}
			for _, n := range idx {
				byID[n.ID] = n
			}
			filtered := nodes[:0]
			for _, n := range nodes {
				if _, ok := byID[n.ID]; ok {
					filtered = append(filtered, n)
				}
			}
			nodes = filtered
		} else if s.Where != nil {
			filtered := nodes[:0]
			for _, n := range nodes {
				ok, err := matchWhere(n.Props, s.Where)
				if err != nil {
					return Result{}, err
				}
				if ok {
					filtered = append(filtered, n)
				}
			}
			nodes = filtered
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		return Result{Kind: "nodes", Nodes: nodes, Message: fmt.Sprintf("%d node(s)", len(nodes))}, nil
	case MatchEdgeStmt:
		edges, err := filterEdges(e.G.EdgesByLabel(s.Label), s.Where)
		if err != nil {
			return Result{}, err
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
		return Result{Kind: "edges", Edges: edges, Message: fmt.Sprintf("%d edge(s)", len(edges))}, nil
	case EdgesStmt:
		edges, err := e.G.EdgesBetween(s.From, s.To)
		if err != nil {
			return Result{}, err
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
		return Result{Kind: "edges", Edges: edges, Message: fmt.Sprintf("%d edge(s)", len(edges))}, nil
	case NeighborsStmt:
		ns, err := e.G.Neighbors(s.ID, s.Depth)
		if err != nil {
			return Result{}, err
		}
		sort.Slice(ns, func(i, j int) bool {
			if ns[i].Depth != ns[j].Depth {
				return ns[i].Depth < ns[j].Depth
			}
			return ns[i].Node.ID < ns[j].Node.ID
		})
		return Result{Kind: "neighbors", Neighbors: ns, Message: fmt.Sprintf("%d neighbor(s)", len(ns))}, nil
	case PathStmt:
		path, err := e.G.ShortestPath(s.From, s.To)
		if err != nil {
			return Result{}, err
		}
		msg := "no path"
		if len(path) > 0 {
			ids := make([]string, len(path))
			for i, n := range path {
				ids[i] = fmt.Sprintf("%d", n.ID)
			}
			msg = "path " + strings.Join(ids, " -> ")
		}
		return Result{Kind: "path", Path: path, Message: msg}, nil
	case GetNodeStmt:
		n, ok := e.G.GetNode(s.ID)
		if !ok {
			return Result{}, gerr.Newf(gerr.NotFound, "node %d not found", s.ID)
		}
		return Result{Kind: "nodes", Nodes: []graph.Node{n}, Message: fmt.Sprintf("node %d", n.ID)}, nil
	case GetEdgeStmt:
		ed, ok := e.G.GetEdge(s.ID)
		if !ok {
			return Result{}, gerr.Newf(gerr.NotFound, "edge %d not found", s.ID)
		}
		return Result{Kind: "edges", Edges: []graph.Edge{ed}, Message: fmt.Sprintf("edge %d", ed.ID)}, nil
	case DeleteNodeStmt:
		if err := e.G.DeleteNode(s.ID); err != nil {
			return Result{}, err
		}
		return Result{Kind: "message", Message: fmt.Sprintf("deleted node %d", s.ID), Mutating: true}, nil
	case DeleteEdgeStmt:
		if err := e.G.DeleteEdge(s.ID); err != nil {
			return Result{}, err
		}
		return Result{Kind: "message", Message: fmt.Sprintf("deleted edge %d", s.ID), Mutating: true}, nil
	case ShowStatsStmt:
		st := e.G.Stats()
		sort.Strings(st.Labels)
		return Result{Kind: "stats", Stats: st, Message: fmt.Sprintf("nodes=%d edges=%d", st.Nodes, st.Edges)}, nil
	case HelpStmt:
		return Result{Kind: "help", Message: helpText}, nil
	case ExitStmt:
		return Result{Kind: "exit", Message: "bye", Exit: true}, nil
	case SaveStmt:
		if err := persist.Save(e.G, s.Path); err != nil {
			return Result{}, err
		}
		if e.WAL != nil {
			if err := e.WAL.Truncate(); err != nil {
				return Result{}, fmt.Errorf("truncate wal: %w", err)
			}
		}
		return Result{Kind: "message", Message: "saved " + s.Path}, nil
	case LoadStmt:
		if err := persist.Load(e.G, s.Path); err != nil {
			return Result{}, err
		}
		if e.WAL != nil {
			if err := e.WAL.Truncate(); err != nil {
				return Result{}, fmt.Errorf("truncate wal: %w", err)
			}
		}
		return Result{Kind: "message", Message: "loaded " + s.Path}, nil
	default:
		return Result{}, gerr.New(gerr.Internal, "unhandled statement")
	}
}

func filterEdges(edges []graph.Edge, w *Where) ([]graph.Edge, error) {
	if w == nil {
		return edges, nil
	}
	out := edges[:0]
	for _, e := range edges {
		ok, err := matchWhere(e.Props, w)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, e)
		}
	}
	return out, nil
}

const helpText = `Commands:
  CREATE NODE <label> {key: value, ...}
  CREATE EDGE <from> -<LABEL>-> <to> {key: value, ...}
  UPDATE NODE <id> {key: value, ...}
  UPDATE EDGE <id> {key: value, ...}
  MATCH <label> [WHERE <key> <op> <value>]
  MATCH EDGE <label> [WHERE <key> <op> <value>]
  GET NODE <id>
  GET EDGE <id>
  EDGES <from> TO <to>
  NEIGHBORS <id> [DEPTH <n>]
  PATH <from> TO <to>
  DELETE NODE <id>
  DELETE EDGE <id>
  SHOW STATS
  SAVE <file>
  LOAD <file>
  HELP
  EXIT

Operators: = != > < >= <=
Values: strings ("Alice"), integers, floats, true/false
`

package query

import (
	"path/filepath"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/persist"
)

func TestParseTable(t *testing.T) {
	tests := []struct {
		in   string
		kind string
		fail bool
	}{
		{in: `CREATE NODE person {name: "Alice", age: 30}`, kind: "CreateNodeStmt"},
		{in: `CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`, kind: "CreateEdgeStmt"},
		{in: `MATCH person WHERE age > 25`, kind: "MatchStmt"},
		{in: `MATCH person`, kind: "MatchStmt"},
		{in: `MATCH EDGE KNOWS`, kind: "MatchEdgeStmt"},
		{in: `MATCH EDGE KNOWS WHERE since > 2019`, kind: "MatchEdgeStmt"},
		{in: `EDGES 1 TO 2`, kind: "EdgesStmt"},
		{in: `GET NODE 1`, kind: "GetNodeStmt"},
		{in: `GET EDGE 1`, kind: "GetEdgeStmt"},
		{in: `NEIGHBORS 1 DEPTH 2`, kind: "NeighborsStmt"},
		{in: `NEIGHBORS 1`, kind: "NeighborsStmt"},
		{in: `PATH 1 TO 5`, kind: "PathStmt"},
		{in: `DELETE NODE 1`, kind: "DeleteNodeStmt"},
		{in: `DELETE EDGE 1`, kind: "DeleteEdgeStmt"},
		{in: `UPDATE NODE 1 {age: 31}`, kind: "UpdateNodeStmt"},
		{in: `SHOW STATS`, kind: "ShowStatsStmt"},
		{in: `HELP`, kind: "HelpStmt"},
		{in: `EXIT`, kind: "ExitStmt"},
		{in: `SAVE graph.db`, kind: "SaveStmt"},
		{in: `LOAD "my graph.db"`, kind: "LoadStmt"},
		{in: `create node person`, kind: "CreateNodeStmt"},
		{in: ``, fail: true},
		{in: `FLORB`, fail: true},
		{in: `CREATE EDGE 1 KNOWS 2`, fail: true},
		{in: `MATCH person WHERE`, fail: true},
	}
	for _, tc := range tests {
		stmt, err := Parse(tc.in)
		if tc.fail {
			if err == nil {
				t.Fatalf("%q: expected error, got %#v", tc.in, stmt)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		got := typeName(stmt)
		if got != tc.kind {
			t.Fatalf("%q: got %s want %s", tc.in, got, tc.kind)
		}
	}
}

func typeName(s Stmt) string {
	switch s.(type) {
	case CreateNodeStmt:
		return "CreateNodeStmt"
	case CreateEdgeStmt:
		return "CreateEdgeStmt"
	case UpdateNodeStmt:
		return "UpdateNodeStmt"
	case UpdateEdgeStmt:
		return "UpdateEdgeStmt"
	case MatchStmt:
		return "MatchStmt"
	case MatchEdgeStmt:
		return "MatchEdgeStmt"
	case EdgesStmt:
		return "EdgesStmt"
	case GetNodeStmt:
		return "GetNodeStmt"
	case GetEdgeStmt:
		return "GetEdgeStmt"
	case NeighborsStmt:
		return "NeighborsStmt"
	case PathStmt:
		return "PathStmt"
	case DeleteNodeStmt:
		return "DeleteNodeStmt"
	case DeleteEdgeStmt:
		return "DeleteEdgeStmt"
	case ShowStatsStmt:
		return "ShowStatsStmt"
	case HelpStmt:
		return "HelpStmt"
	case ExitStmt:
		return "ExitStmt"
	case SaveStmt:
		return "SaveStmt"
	case LoadStmt:
		return "LoadStmt"
	default:
		return "unknown"
	}
}

func TestParseCreateNodeProps(t *testing.T) {
	stmt, err := Parse(`CREATE NODE person {name: "Alice", age: 30, ok: true}`)
	if err != nil {
		t.Fatal(err)
	}
	n := stmt.(CreateNodeStmt)
	if n.Label != "person" || n.Props["name"] != "Alice" || n.Props["age"] != int64(30) || n.Props["ok"] != true {
		t.Fatalf("props: %+v", n.Props)
	}
}

func TestExecCRUDAndMatch(t *testing.T) {
	e := NewExecutor(graph.New(), nil)
	mustExec(t, e, `CREATE NODE person {name: "Alice", age: 30}`)
	mustExec(t, e, `CREATE NODE person {name: "Bob", age: 20}`)
	mustExec(t, e, `CREATE NODE company {name: "Acme"}`)
	mustExec(t, e, `CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`)

	res, err := e.ExecString(`MATCH person WHERE age > 25`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || !nodeProp(res.Nodes[0], "name", "Alice") {
		t.Fatalf("match >: %+v", res.Nodes)
	}

	res, err = e.ExecString(`MATCH person WHERE name = "Bob"`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != 2 {
		t.Fatalf("match =: %+v", res.Nodes)
	}

	res, err = e.ExecString(`NEIGHBORS 1 DEPTH 1`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Neighbors) != 1 || res.Neighbors[0].Node.ID != 2 {
		t.Fatalf("neighbors: %+v", res.Neighbors)
	}

	res, err = e.ExecString(`PATH 1 TO 2`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Path) != 2 {
		t.Fatalf("path: %+v", res.Path)
	}

	res, err = e.ExecString(`EDGES 1 TO 2`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].Label != "KNOWS" || !edgeProp(res.Edges[0], "since", int64(2020)) {
		t.Fatalf("edges between: %+v", res.Edges)
	}

	res, err = e.ExecString(`MATCH EDGE KNOWS WHERE since >= 2020`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].From != 1 || res.Edges[0].To != 2 {
		t.Fatalf("match edge: %+v", res.Edges)
	}

	res, err = e.ExecString(`GET NODE 1`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || !nodeProp(res.Nodes[0], "name", "Alice") {
		t.Fatalf("get node: %+v", res.Nodes)
	}

	res, err = e.ExecString(`GET EDGE 1`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].Label != "KNOWS" {
		t.Fatalf("get edge: %+v", res.Edges)
	}

	if _, err := e.ExecString(`GET NODE 99`, false); err == nil {
		t.Fatal("expected missing node error")
	} else if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}

	mustExec(t, e, `DELETE NODE 1`)
	if e.G.Stats().Edges != 0 {
		t.Fatal("edge should cascade-delete")
	}
}

func TestSaveLoadViaExecutor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "g.db")
	e := NewExecutor(graph.New(), nil)
	mustExec(t, e, `CREATE NODE person {name: "Alice"}`)
	mustExec(t, e, `CREATE NODE person {name: "Bob"}`)
	mustExec(t, e, `CREATE EDGE 1 -KNOWS-> 2`)
	if _, err := e.ExecString(`SAVE `+path, false); err != nil {
		t.Fatal(err)
	}

	e2 := NewExecutor(graph.New(), nil)
	if _, err := e2.ExecString(`LOAD `+path, false); err != nil {
		t.Fatal(err)
	}
	res, err := e2.ExecString(`MATCH person`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("loaded %d nodes", len(res.Nodes))
	}
}

func TestWALReplay(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "g.wal")
	wal, err := persist.OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(graph.New(), wal)
	mustExec(t, e, `CREATE NODE person {name: "Alice"}`)
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	lines, err := persist.ReadWAL(walPath)
	if err != nil || len(lines) != 1 {
		t.Fatalf("wal lines=%v err=%v", lines, err)
	}

	e2 := NewExecutor(graph.New(), nil)
	if _, err := e2.ExecString(lines[0], true); err != nil {
		t.Fatal(err)
	}
	if e2.G.Stats().Nodes != 1 {
		t.Fatal("replay failed")
	}
}

func TestLexerErrors(t *testing.T) {
	if _, err := Parse(`CREATE NODE x {name: "unterminated}`); err == nil {
		t.Fatal("expected unterminated string")
	} else if !gerr.IsCode(err, gerr.Syntax) {
		t.Fatalf("expected Syntax, got %v", err)
	}
}

func TestParseSyntaxCode(t *testing.T) {
	if _, err := Parse(""); !gerr.IsCode(err, gerr.Syntax) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Parse("FLORB"); !gerr.IsCode(err, gerr.Syntax) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestHelpAndExit(t *testing.T) {
	e := NewExecutor(graph.New(), nil)
	h, err := e.ExecString("HELP", false)
	if err != nil || h.Kind != "help" {
		t.Fatalf("help: %+v %v", h, err)
	}
	x, err := e.ExecString("QUIT", false)
	if err != nil || !x.Exit {
		t.Fatalf("quit: %+v %v", x, err)
	}
}

func nodeProp(n graph.Node, key, want string) bool {
	v, ok := n.Prop(key)
	if !ok {
		return false
	}
	s, ok := v.StringValue()
	return ok && s == want
}

func edgeProp(e graph.Edge, key string, want int64) bool {
	v, ok := e.Prop(key)
	if !ok {
		return false
	}
	i, ok := v.IntValue()
	return ok && i == want
}

func mustExec(t *testing.T, e *Executor, line string) {
	t.Helper()
	if _, err := e.ExecString(line, false); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
}

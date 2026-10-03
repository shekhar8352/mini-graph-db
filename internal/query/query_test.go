package query

import (
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
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
		{in: `BEGIN`, kind: "BeginStmt"},
		{in: `BEGIN READ ONLY`, kind: "BeginStmt"},
		{in: `begin read only`, kind: "BeginStmt"},
		{in: `COMMIT`, kind: "CommitStmt"},
		{in: `ROLLBACK`, kind: "RollbackStmt"},
		{in: `VACUUM`, kind: "VacuumStmt"},
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
	case BeginStmt:
		return "BeginStmt"
	case CommitStmt:
		return "CommitStmt"
	case RollbackStmt:
		return "RollbackStmt"
	case VacuumStmt:
		return "VacuumStmt"
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
	e := NewExecutor(graph.New())
	mustExec(t, e, `CREATE NODE person {name: "Alice", age: 30}`)
	mustExec(t, e, `CREATE NODE person {name: "Bob", age: 20}`)
	mustExec(t, e, `CREATE NODE company {name: "Acme"}`)
	mustExec(t, e, `CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`)

	res, err := e.ExecString(`MATCH person WHERE age > 25`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || !nodeProp(res.Nodes[0], "name", "Alice") {
		t.Fatalf("match >: %+v", res.Nodes)
	}

	res, err = e.ExecString(`MATCH person WHERE name = "Bob"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != 2 {
		t.Fatalf("match =: %+v", res.Nodes)
	}

	res, err = e.ExecString(`NEIGHBORS 1 DEPTH 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Neighbors) != 1 || res.Neighbors[0].Node.ID != 2 {
		t.Fatalf("neighbors: %+v", res.Neighbors)
	}

	res, err = e.ExecString(`PATH 1 TO 2`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Path) != 2 {
		t.Fatalf("path: %+v", res.Path)
	}

	res, err = e.ExecString(`EDGES 1 TO 2`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].Label != "KNOWS" || !edgeProp(res.Edges[0], "since", int64(2020)) {
		t.Fatalf("edges between: %+v", res.Edges)
	}

	res, err = e.ExecString(`MATCH EDGE KNOWS WHERE since >= 2020`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].From != 1 || res.Edges[0].To != 2 {
		t.Fatalf("match edge: %+v", res.Edges)
	}

	res, err = e.ExecString(`GET NODE 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || !nodeProp(res.Nodes[0], "name", "Alice") {
		t.Fatalf("get node: %+v", res.Nodes)
	}

	res, err = e.ExecString(`GET EDGE 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 || res.Edges[0].Label != "KNOWS" {
		t.Fatalf("get edge: %+v", res.Edges)
	}

	if _, err := e.ExecString(`GET NODE 99`); err == nil {
		t.Fatal("expected missing node error")
	} else if !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}

	mustExec(t, e, `DELETE NODE 1`)
	if e.G.Stats().Edges != 0 {
		t.Fatal("edge should cascade-delete")
	}
}

func TestSaveLoadRejected(t *testing.T) {
	e := NewExecutor(graph.New())
	for _, line := range []string{`SAVE graph.db`, `LOAD "my graph.db"`} {
		_, err := e.ExecString(line)
		if err == nil || !strings.Contains(err.Error(), "use `graphdb backup`") {
			t.Fatalf("%s: %v", line, err)
		}
		if !gerr.IsCode(err, gerr.InvalidArgument) {
			t.Fatalf("%s code: %v", line, err)
		}
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
	e := NewExecutor(graph.New())
	h, err := e.ExecString("HELP")
	if err != nil || h.Kind != "help" {
		t.Fatalf("help: %+v %v", h, err)
	}
	if !strings.Contains(h.Message, "BEGIN") || !strings.Contains(h.Message, "VACUUM") {
		t.Fatalf("help missing transactions: %s", h.Message)
	}
	x, err := e.ExecString("QUIT")
	if err != nil || !x.Exit {
		t.Fatalf("quit: %+v %v", x, err)
	}
}

func TestExplicitTransaction(t *testing.T) {
	e := NewExecutor(graph.New())
	mustExec(t, e, `BEGIN`)
	mustExec(t, e, `CREATE NODE person {name: "Ada"}`)
	mustExec(t, e, `ROLLBACK`)
	if _, err := e.ExecString(`GET NODE 1`); !gerr.IsCode(err, gerr.NotFound) {
		t.Fatalf("rollback leaked the node: %v", err)
	}

	mustExec(t, e, `BEGIN`)
	mustExec(t, e, `CREATE NODE person {name: "Ada"}`)
	res, err := e.ExecString(`GET NODE 1`)
	if err != nil || len(res.Nodes) != 1 {
		t.Fatalf("read own write: %+v %v", res, err)
	}
	mustExec(t, e, `COMMIT`)
	res, err = e.ExecString(`GET NODE 1`)
	if err != nil || len(res.Nodes) != 1 || !nodeProp(res.Nodes[0], "name", "Ada") {
		t.Fatalf("committed: %+v %v", res, err)
	}

	mustExec(t, e, `BEGIN READ ONLY`)
	if _, err := e.ExecString(`CREATE NODE person {name: "Bea"}`); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("read-only write: %v", err)
	}
	res, err = e.ExecString(`GET NODE 1`)
	if err != nil || len(res.Nodes) != 1 {
		t.Fatalf("read-only read: %+v %v", res, err)
	}
	mustExec(t, e, `ROLLBACK`)

	if _, err := e.ExecString(`COMMIT`); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("commit without begin: %v", err)
	}
	if _, err := e.ExecString(`BEGIN READ`); err == nil {
		t.Fatal("BEGIN READ should fail")
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
	if _, err := e.ExecString(line); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
}

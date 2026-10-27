package legacy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/exec"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
)

func TestRewriteTable(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`CREATE NODE person {name: "Alice", age: 30}`, `CREATE (:person {age: 30, name: "Alice"})`},
		{`CREATE NODE city {name: Paris}`, `CREATE (:city {name: "Paris"})`},
		{`CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`, `MATCH (a), (b) WHERE id(a) = 1 AND id(b) = 2 CREATE (a)-[:KNOWS {since: 2020}]->(b)`},
		{`MATCH person WHERE age > 25`, `MATCH (n:person) WHERE n.age > 25 RETURN n`},
		{`MATCH EDGE KNOWS WHERE since >= 2020`, `MATCH ()-[e:KNOWS]->() WHERE e.since >= 2020 RETURN e`},
		{`GET NODE 1`, `MATCH (n) WHERE id(n) = 1 RETURN n`},
		{`GET EDGE 1`, `MATCH ()-[e]->() WHERE id(e) = 1 RETURN e`},
		{`EDGES 1 TO 2`, `MATCH (a)-[e]->(b) WHERE id(a) = 1 AND id(b) = 2 RETURN e`},
		{`NEIGHBORS 1 DEPTH 2`, `MATCH (n) WHERE id(n) = 1 UNWIND neighbors(n, 2) AS m RETURN m`},
		{`NEIGHBORS 1`, `MATCH (n) WHERE id(n) = 1 UNWIND neighbors(n, 1) AS m RETURN m`},
		{`PATH 1 TO 2`, `MATCH (a), (b) WHERE id(a) = 1 AND id(b) = 2 RETURN shortestPath(a, b)`},
		{`UPDATE NODE 1 {age: 31}`, `MATCH (n) WHERE id(n) = 1 SET n += {age: 31}`},
		{`UPDATE EDGE 1 {since: 2021}`, `MATCH ()-[e]->() WHERE id(e) = 1 SET e += {since: 2021}`},
		{`DELETE NODE 1`, `MATCH (n) WHERE id(n) = 1 DETACH DELETE n`},
		{`DELETE EDGE 1`, `MATCH ()-[e]->() WHERE id(e) = 1 DELETE e`},
		{`SHOW STATS`, `SHOW STATS`},
		{`BEGIN`, `BEGIN`},
		{`BEGIN READ ONLY`, `BEGIN READ ONLY`},
		{`COMMIT`, `COMMIT`},
		{`ROLLBACK`, `ROLLBACK`},
		{`VACUUM`, `VACUUM`},
	}
	for _, tc := range cases {
		got, err := Rewrite(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if !got.Legacy || got.Message == "" {
			t.Fatalf("%s: not marked legacy %+v", tc.in, got)
		}
		want, err := parser.Parse(tc.want)
		if err != nil {
			t.Fatalf("want %s: %v", tc.want, err)
		}
		if !reflect.DeepEqual(got.Script, want) {
			t.Fatalf("%s\ngot  %s\nwant %s", tc.in, ast.Format(got.Script), ast.Format(want))
		}
	}
}

func TestCompilePrefersGQL(t *testing.T) {
	got, err := Compile(`MATCH (n:Person) RETURN n.name AS name`)
	if err != nil || got.Legacy || got.Source != `MATCH (n:Person) RETURN n.name AS name` {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCompileLegacyCreate(t *testing.T) {
	got, err := Compile(`CREATE NODE person {name: "Ada"}`)
	if err != nil || !got.Legacy {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.Contains(got.Source, `:person`) {
		t.Fatalf("source %s", got.Source)
	}
}

func TestHelpExitSave(t *testing.T) {
	h, err := Compile("HELP")
	if err != nil || !h.Help {
		t.Fatalf("help %+v %v", h, err)
	}
	x, err := Compile("QUIT")
	if err != nil || !x.Exit {
		t.Fatalf("quit %+v %v", x, err)
	}
	_, err = Compile(`SAVE graph.db`)
	if err == nil || gerr.CodeOf(err) != gerr.InvalidArgument || !strings.Contains(err.Error(), "use `graphdb backup`") {
		t.Fatalf("save %v", err)
	}
	_, err = Compile(`LOAD "x.db"`)
	if err == nil || gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatalf("load %v", err)
	}
}

func TestRewriteRuns(t *testing.T) {
	g := graph.New()
	for _, line := range []string{
		`CREATE NODE person {name: "Alice", age: 30}`,
		`CREATE NODE person {name: "Bob", age: 20}`,
		`CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`,
		`MATCH person WHERE age > 25`,
	} {
		got, err := Rewrite(line)
		if err != nil {
			t.Fatal(err)
		}
		res, err := exec.Run(context.Background(), g, got.Source, exec.Options{
			Notifications: []exec.Notification{{Code: exec.DeprecatedSyntax, Message: got.Message}},
		})
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if len(res.Notifications) != 1 || res.Notifications[0].Code != exec.DeprecatedSyntax {
			t.Fatalf("notice %+v", res.Notifications)
		}
	}
	if g.Stats().Nodes != 2 || g.Stats().Edges != 1 {
		t.Fatalf("stats %+v", g.Stats())
	}
}

func TestReservedLabel(t *testing.T) {
	got, err := Rewrite(`CREATE NODE match {name: "x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Source, "`match`") {
		t.Fatalf("source %s", got.Source)
	}
}

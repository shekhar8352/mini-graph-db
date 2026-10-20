package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestReadPipeline(t *testing.T) {
	g := graph.New()
	alice := g.AddNode("Person", map[string]any{"name": "Alice", "city": "Paris"})
	bob := g.AddNode("Person", map[string]any{"name": "Bob", "city": "Paris"})
	g.AddNode("Person", map[string]any{"name": "Zoe", "city": "Lima"})
	g.AddNode("City", map[string]any{"name": "Paris"})
	if _, err := g.AddEdge(alice.ID, bob.ID, "KNOWS", map[string]any{"since": 2020}); err != nil {
		t.Fatal(err)
	}

	cols, rows := mustRun(t, g, `MATCH (a:Person {name: "Alice"}) RETURN a.name AS name`, Options{})
	if cols[0] != "name" || len(rows) != 1 || str(rows[0][0]) != "Alice" {
		t.Fatalf("seek %v %v", cols, rows)
	}

	_, rows = mustRun(t, g, `MATCH (a:Person)-[:KNOWS]->(b) RETURN b.name AS name`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "Bob" {
		t.Fatalf("expand %#v", rows)
	}

	_, rows = mustRun(t, g, `MATCH (a:Person), (c:City) WHERE a.city = c.name RETURN a.name AS name`, Options{})
	if len(rows) != 2 {
		t.Fatalf("comma %d", len(rows))
	}

	_, rows = mustRun(t, g, `MATCH (a:Person) OPTIONAL MATCH (a)-[:LIVES_IN]->(c) RETURN a.name AS name, c.name AS city`, Options{})
	if len(rows) != 3 || rows[0][1].Kind() != value.KindNull {
		t.Fatalf("optional %#v", rows)
	}

	_, rows = mustRun(t, g, `MATCH (a:Person) RETURN count(*) AS n`, Options{})
	if n, _ := rows[0][0].IntValue(); len(rows) != 1 || n != 3 {
		t.Fatalf("count %#v", rows)
	}

	_, rows = mustRun(t, g, `MATCH (a:Person) RETURN a.city AS city, count(*) AS n ORDER BY city`, Options{})
	if len(rows) != 2 || str(rows[0][0]) != "Lima" || str(rows[1][0]) != "Paris" {
		t.Fatalf("group %#v", rows)
	}

	_, rows = mustRun(t, g, `UNWIND [3, 1, 2, 1] AS x RETURN DISTINCT x ORDER BY x SKIP 1 LIMIT 1`, Options{})
	if n, _ := rows[0][0].IntValue(); len(rows) != 1 || n != 2 {
		t.Fatalf("pipeline %#v", rows)
	}

	_, rows = mustRun(t, g, `RETURN 1 AS n UNION RETURN 1 AS n`, Options{})
	if len(rows) != 1 {
		t.Fatalf("union %d", len(rows))
	}
	_, rows = mustRun(t, g, `RETURN 1 AS n UNION ALL RETURN 1 AS n`, Options{})
	if len(rows) != 2 {
		t.Fatalf("union all %d", len(rows))
	}

	_, rows = mustRun(t, g, `CALL db.labels() YIELD label RETURN label`, Options{})
	if len(rows) != 2 || str(rows[0][0]) != "City" || str(rows[1][0]) != "Person" {
		t.Fatalf("labels %#v", rows)
	}
}

func TestExpandAndShortest(t *testing.T) {
	g := graph.New()
	a := g.AddNode("Person", map[string]any{"name": "A"})
	b := g.AddNode("Person", map[string]any{"name": "B"})
	c := g.AddNode("Person", map[string]any{"name": "C"})
	if _, err := g.AddEdge(a.ID, b.ID, "KNOWS", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge(b.ID, c.ID, "KNOWS", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge(a.ID, c.ID, "KNOWS", nil); err != nil {
		t.Fatal(err)
	}
	_, rows := mustRun(t, g, `MATCH (a {name: "A"})-[k:KNOWS*1..2]->(c {name: "C"}) RETURN size(k) AS n ORDER BY n`, Options{})
	if len(rows) != 2 {
		t.Fatalf("varlen %d", len(rows))
	}
	n0, _ := rows[0][0].IntValue()
	n1, _ := rows[1][0].IntValue()
	if n0 != 1 || n1 != 2 {
		t.Fatalf("hops %d %d", n0, n1)
	}

	_, rows = mustRun(t, g, `MATCH (a {name: "A"}), (c {name: "C"}) MATCH p = shortestPath((a)-[:KNOWS*]->(c)) RETURN length(p) AS n`, Options{})
	if n, _ := rows[0][0].IntValue(); len(rows) != 1 || n != 1 {
		t.Fatalf("shortest %#v", rows)
	}
}

func TestMutation(t *testing.T) {
	g := graph.New()
	cols, rows := mustRun(t, g, `CREATE (a:Person {name: "Ada"})-[:KNOWS]->(b:Person {name: "Bea"}) RETURN a.name AS name`, Options{})
	if cols[0] != "name" || str(rows[0][0]) != "Ada" {
		t.Fatalf("create %#v", rows)
	}
	if _, rows := mustRun(t, g, `MATCH (a:Person) RETURN count(*) AS n`, Options{}); !value.Equal(rows[0][0], value.Int(2)) {
		t.Fatalf("created %v", rows)
	}
	mustRun(t, g, `MATCH (a {name: "Ada"}) SET a.name = null, a:Admin REMOVE a:Person RETURN a`, Options{})
	node := g.AllNodes()[0]
	if _, ok := node.Prop("name"); ok {
		t.Fatal("name remains")
	}
	if node.HasLabel("Person") || !node.HasLabel("Admin") {
		t.Fatalf("labels %v", node.Labels())
	}

	_, _, err := Run(context.Background(), g, `MATCH (a:Admin) DELETE a`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("delete %v", err)
	}
	mustRun(t, g, `MATCH (a:Admin) DETACH DELETE a`, Options{})
	if g.Stats().Nodes != 1 {
		t.Fatalf("nodes %d", g.Stats().Nodes)
	}

	g2 := graph.New()
	mustRun(t, g2, `MERGE (a:Person {name: "Ada"}) ON CREATE SET a.n = 1 RETURN a.n`, Options{})
	_, rows = mustRun(t, g2, `MERGE (a:Person {name: "Ada"}) ON MATCH SET a.n = 2 RETURN a.n AS n`, Options{})
	if !value.Equal(rows[0][0], value.Int(2)) || g2.Stats().Nodes != 1 {
		t.Fatalf("merge %#v nodes %d", rows, g2.Stats().Nodes)
	}

	g3 := graph.New()
	cols, rows = mustRun(t, g3, `CREATE (a:Person {name: "Z"})`, Options{})
	if cols != nil || rows != nil {
		t.Fatalf("write rows %v %v", cols, rows)
	}
	_, rows = mustRun(t, g3, `MATCH (a:Person) RETURN a.name AS name`, Options{})
	if str(rows[0][0]) != "Z" {
		t.Fatalf("persisted %#v", rows)
	}
}

func TestBudgetCancelAndHops(t *testing.T) {
	g := graph.New()
	_, _, err := Run(context.Background(), g, `UNWIND [1, 2, 3] AS x RETURN x ORDER BY x`, Options{Budget: 1})
	if gerr.CodeOf(err) != gerr.ResourceExhausted {
		t.Fatalf("budget %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = Run(ctx, g, `RETURN 1`, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel %v", err)
	}

	ids := make([]graph.Node, 5)
	for i := range ids {
		ids[i] = g.AddNode("N", nil)
		if i > 0 {
			if _, err := g.AddEdge(ids[i-1].ID, ids[i].ID, "NEXT", nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, _, err = Run(context.Background(), g, `MATCH (a)-[*]->(b) RETURN b`, Options{MaxHops: 3})
	if gerr.CodeOf(err) != gerr.ResourceExhausted {
		t.Fatalf("hops %v", err)
	}
}

func TestParamsAndReadOnly(t *testing.T) {
	g := graph.New()
	g.AddNode("Person", map[string]any{"name": "Ada"})
	_, rows := mustRun(t, g, `MATCH (a:Person) WHERE a.name = $name RETURN a.name AS name`, Options{
		Params: map[string]value.Value{"name": value.String("Ada")},
	})
	if str(rows[0][0]) != "Ada" {
		t.Fatalf("param %#v", rows)
	}
	mustRun(t, g, `BEGIN READ ONLY`, Options{})
	_, _, err := Run(context.Background(), g, `CREATE (a:Person)`, Options{})
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatalf("readonly %v", err)
	}
}

func mustRun(t *testing.T, g *graph.Graph, src string, opt Options) ([]string, [][]value.Value) {
	t.Helper()
	cols, rows, err := Run(context.Background(), g, src, opt)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return cols, rows
}

func str(v value.Value) string {
	s, _ := v.StringValue()
	return s
}

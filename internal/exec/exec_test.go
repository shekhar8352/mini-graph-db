package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/txn"
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

	_, err := Run(context.Background(), g, `MATCH (a:Admin) DELETE a`, Options{})
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
	_, err := Run(context.Background(), g, `UNWIND [1, 2, 3] AS x RETURN x ORDER BY x`, Options{Budget: 1})
	if gerr.CodeOf(err) != gerr.ResourceExhausted {
		t.Fatalf("budget %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Run(ctx, g, `RETURN 1`, Options{})
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
	_, err = Run(context.Background(), g, `MATCH (a)-[*]->(b) RETURN b`, Options{MaxHops: 3})
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
	_, err := Run(context.Background(), g, `CREATE (a:Person)`, Options{})
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatalf("readonly %v", err)
	}
}

func TestShowAndAdminLeaves(t *testing.T) {
	g := graph.New()
	g.AddNode("Person", map[string]any{"name": "Ada"})
	cols, rows := mustRun(t, g, `SHOW STATS`, Options{})
	if len(cols) != 3 || cols[0] != "nodes" || len(rows) != 1 {
		t.Fatalf("stats %v %v", cols, rows)
	}
	if n, _ := rows[0][0].IntValue(); n != 1 {
		t.Fatalf("nodes %v", rows[0][0])
	}
	cols, rows = mustRun(t, g, `SHOW LABELS`, Options{})
	if len(cols) != 1 || cols[0] != "name" || len(rows) != 1 || str(rows[0][0]) != "Person" {
		t.Fatalf("labels %v %v", cols, rows)
	}
	_, err := Run(context.Background(), g, `CREATE USER alice SET PASSWORD "s3cret"`, Options{})
	if gerr.CodeOf(err) != gerr.Unavailable {
		t.Fatalf("dcl %v", err)
	}
	_, err = Run(context.Background(), g, `SHOW USERS`, Options{})
	if gerr.CodeOf(err) != gerr.Unavailable {
		t.Fatalf("show users %v", err)
	}
}

func TestRunRecoversPanic(t *testing.T) {
	res, err := Run(context.Background(), nil, `RETURN 1`, Options{})
	if res != nil || gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatalf("nil graph %v %v", res, err)
	}
}

func mustRun(t *testing.T, g *graph.Graph, src string, opt Options) ([]string, [][]value.Value) {
	t.Helper()
	res, err := Run(context.Background(), g, src, opt)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return res.Columns, res.All()
}

func str(v value.Value) string {
	s, _ := v.StringValue()
	return s
}

func sawName(rows [][]value.Value, name string) bool {
	for _, r := range rows {
		if len(r) > 0 && str(r[0]) == name {
			return true
		}
	}
	return false
}

func sawNull(rows [][]value.Value) bool {
	for _, r := range rows {
		if len(r) > 0 && r[0].Kind() == value.KindNull {
			return true
		}
	}
	return false
}

func TestStatementAtomicity(t *testing.T) {
	g := graph.New()
	_, err := Run(context.Background(), g, `
		CREATE (a:Person {name: "A"})-[:KNOWS]->(b:Person);
		CREATE (c:Person)-[:KNOWS]->(d:Person) DELETE c;
		CREATE (e:Person {name: "E"})
	`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("script %v", err)
	}
	if g.InTransaction() {
		t.Fatal("auto-commit left a transaction open")
	}
	_, rows := mustRun(t, g, `MATCH (n:Person) RETURN n.name AS name`, Options{})
	if len(rows) != 2 || !sawName(rows, "A") || !sawNull(rows) {
		t.Fatalf("committed %#v", rows)
	}
	if g.Stats().Nodes != 2 || g.Stats().Edges != 1 {
		t.Fatalf("stats %+v", g.Stats())
	}

	g = graph.New()
	mustRun(t, g, `BEGIN`, Options{})
	mustRun(t, g, `CREATE (a:Person {name: "A"})`, Options{})
	_, err = Run(context.Background(), g, `CREATE (b:Person)-[:KNOWS]->(c:Person) DELETE b`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("inner %v", err)
	}
	if !g.InTransaction() {
		t.Fatal("explicit transaction closed")
	}
	_, rows = mustRun(t, g, `MATCH (n:Person) RETURN n.name AS name`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "A" {
		t.Fatalf("kept %#v", rows)
	}
	mustRun(t, g, `COMMIT`, Options{})
	if g.InTransaction() || g.Stats().Nodes != 1 {
		t.Fatalf("after commit open=%v nodes=%d", g.InTransaction(), g.Stats().Nodes)
	}

	g = graph.New()
	mustRun(t, g, `CREATE (a:Person {name: "Ada", city: "Paris"})`, Options{})
	_, err = Run(context.Background(), g, `MATCH (a {name: "Ada"}) SET a.name = null CREATE (b)-[:KNOWS]->(c) DELETE b`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("set rollback %v", err)
	}
	if _, ok := g.AllNodes()[0].Prop("name"); !ok {
		t.Fatal("name was cleared")
	}

	mustRun(t, g, `MATCH (a {name: "Ada"}) SET a = null`, Options{})
	node := g.AllNodes()[0]
	if len(node.Properties()) != 0 || !node.HasLabel("Person") {
		t.Fatalf("cleared %#v", node.Properties())
	}
	mustRun(t, g, `MATCH (a) SET a.name = "Ada"`, Options{})
	mustRun(t, g, `MATCH (a) DELETE null`, Options{})
	if g.Stats().Nodes != 1 {
		t.Fatalf("delete null %d", g.Stats().Nodes)
	}
	mustRun(t, g, `MATCH (a) DELETE a, a`, Options{})
	if g.Stats().Nodes != 0 {
		t.Fatalf("double delete %d", g.Stats().Nodes)
	}

	g = graph.New()
	mustRun(t, g, `MERGE (a:Person {name: "Ada"})`, Options{})
	mustRun(t, g, `MERGE (a:Person {name: "Ada"}) ON MATCH SET a.seen = a.seen + 1`, Options{})
	if _, ok := g.AllNodes()[0].Prop("seen"); ok {
		t.Fatal("null property remains")
	}

	mustRun(t, g, `BEGIN READ ONLY`, Options{})
	_, err = Run(context.Background(), g, `CREATE (a:Person)`, Options{})
	if gerr.CodeOf(err) != gerr.InvalidArgument || !g.InTransaction() {
		t.Fatalf("readonly %v open=%v", err, g.InTransaction())
	}
	mustRun(t, g, `COMMIT`, Options{})
}

func TestUniqueAtCommit(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Person {email: "a@b.c"})`, Options{})
	mustRun(t, g, `CREATE (b:Person {email: "a@b.c"})`, Options{})
	_, err := Run(context.Background(), g, `CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation || g.Stats().Nodes != 2 {
		t.Fatalf("existing dup %v nodes %d", err, g.Stats().Nodes)
	}
	mustRun(t, g, `CREATE (c:Person {email: "a@b.c"})`, Options{})

	g = graph.New()
	mustRun(t, g, `CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE`, Options{})
	mustRun(t, g, `BEGIN`, Options{})
	mustRun(t, g, `CREATE (a:Person {email: "a@b.c"})`, Options{})
	mustRun(t, g, `CREATE (b:Person {email: "a@b.c"})`, Options{})
	_, err = Run(context.Background(), g, `COMMIT`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation || !g.InTransaction() {
		t.Fatalf("same tx %v open=%v", err, g.InTransaction())
	}
	mustRun(t, g, `ROLLBACK`, Options{})
	if g.Stats().Nodes != 0 {
		t.Fatalf("rolled %d", g.Stats().Nodes)
	}

	eng := memory.Open()
	m, err := txn.Open(eng, txn.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	g1 := graph.NewWith(m)
	g2 := graph.NewWith(m)
	mustRun(t, g1, `CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE`, Options{})
	mustRun(t, g1, `BEGIN`, Options{})
	mustRun(t, g2, `BEGIN`, Options{})
	mustRun(t, g1, `MERGE (a:Person {name: "Ada"}) ON CREATE SET a.email = "a@b.c"`, Options{})
	mustRun(t, g2, `MERGE (a:Person {name: "Ada"}) ON CREATE SET a.email = "a@b.c"`, Options{})
	mustRun(t, g1, `COMMIT`, Options{})
	_, err = Run(context.Background(), g2, `COMMIT`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation || !g2.InTransaction() {
		t.Fatalf("second commit %v open=%v", err, g2.InTransaction())
	}
	mustRun(t, g2, `ROLLBACK`, Options{})
	got := graph.NewWith(m)
	_, rows := mustRun(t, got, `MATCH (a:Person) RETURN a.email AS email`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "a@b.c" {
		t.Fatalf("winner %#v", rows)
	}
}

func TestExplainProfileAndSummary(t *testing.T) {
	g := graph.New()
	explain := `EXPLAIN MATCH (a:Person {name: "Alice"}) RETURN a.name AS name`
	wantPlan := "Project(a.name AS name)\n  IndexSeek((a:Person {name: \"Alice\"}))\n"
	res, err := Run(context.Background(), g, explain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "plan" || len(res.All()) != 1 || str(res.All()[0][0]) != wantPlan {
		t.Fatalf("explain %#v %q", res.Columns, res.All())
	}
	if res.Summary != (Summary{}) || len(res.Profile) != 0 || len(res.Notifications) != 0 || g.Stats().Nodes != 0 {
		t.Fatalf("explain effects %+v profile %d nodes %d", res.Summary, len(res.Profile), g.Stats().Nodes)
	}
	again, err := Run(context.Background(), g, explain, Options{})
	if err != nil || str(again.All()[0][0]) != wantPlan {
		t.Fatalf("unstable %v %q", err, again.All())
	}
	before := g.Stats().Nodes
	if _, err := Run(context.Background(), g, `EXPLAIN CREATE (a:Person {name: "Z"})`, Options{}); err != nil {
		t.Fatal(err)
	}
	if g.Stats().Nodes != before {
		t.Fatalf("explain created a node")
	}

	g.AddNode("Person", map[string]any{"name": "Alice"})
	res, err = Run(context.Background(), g, `PROFILE MATCH (a:Person {name: "Alice"}) RETURN a.name AS name`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "name" || len(res.All()) != 1 || str(res.All()[0][0]) != "Alice" {
		t.Fatalf("profile rows %#v %v", res.Columns, res.All())
	}
	if len(res.Profile) != 2 {
		t.Fatalf("ops %+v", res.Profile)
	}
	root, seek := res.Profile[0], res.Profile[1]
	if root.Text != "Project(a.name AS name)" || root.Depth != 0 || root.Rows != 1 || root.DBHits != 1 {
		t.Fatalf("project %+v", root)
	}
	if seek.Text != `IndexSeek((a:Person {name: "Alice"}))` || seek.Depth != 1 || seek.Rows != 1 || seek.DBHits != 3 {
		t.Fatalf("seek %+v", seek)
	}
	if root.Time < seek.Time || root.Time < 0 || seek.Time < 0 {
		t.Fatalf("time %s %s", root.Time, seek.Time)
	}

	res, err = Run(context.Background(), g, `CREATE (a:Person {name: "Ada"})-[:KNOWS]->(b:Person {name: "Bea"}) RETURN a.name`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != (Summary{NodesCreated: 2, EdgesCreated: 1, PropertiesSet: 2, LabelsAdded: 2}) {
		t.Fatalf("create %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `MATCH (a {name: "Ada"}) SET a.name = "Ada", a:Person, a:Admin`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != (Summary{PropertiesSet: 1, LabelsAdded: 1}) {
		t.Fatalf("set %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `MATCH (a:Admin) REMOVE a:Missing, a:Admin`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != (Summary{LabelsRemoved: 1}) {
		t.Fatalf("remove %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `MATCH (a {name: "Ada"}) SET a.missing = null`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != (Summary{}) {
		t.Fatalf("null set %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `MATCH (a {name: "Ada"}) DETACH DELETE a`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != (Summary{NodesDeleted: 1, EdgesDeleted: 1}) {
		t.Fatalf("detach %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `CREATE (a:Person {name: "C"}) DELETE a, a`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.NodesCreated != 1 || res.Summary.NodesDeleted != 1 || res.Summary.LabelsAdded != 1 || res.Summary.PropertiesSet != 1 {
		t.Fatalf("double delete %+v", res.Summary)
	}
	res, err = Run(context.Background(), g, `CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE`, Options{})
	if err != nil || res.Summary.ConstraintsAdded != 1 {
		t.Fatalf("constraint %v %+v", err, res)
	}
	res, err = Run(context.Background(), g, `DROP CONSTRAINT person_email_unique`, Options{})
	if err != nil || res.Summary.ConstraintsRemoved != 1 {
		t.Fatalf("drop %v %+v", err, res.Summary)
	}
	res, err = Run(context.Background(), g, `DROP CONSTRAINT missing_name`, Options{})
	if gerr.CodeOf(err) != gerr.NotFound {
		t.Fatalf("missing drop %v %+v", err, res)
	}

	res, err = Run(context.Background(), g, `RETURN 1 AS n UNION ALL RETURN 2 AS n`, Options{
		Notifications: []Notification{{Code: DeprecatedSyntax, Message: "legacy create"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := res.Next()
	if !ok || !value.Equal(row[0], value.Int(1)) {
		t.Fatalf("first %v %v", row, ok)
	}
	row, ok = res.Next()
	if !ok || !value.Equal(row[0], value.Int(2)) {
		t.Fatalf("second %v %v", row, ok)
	}
	if _, ok = res.Next(); ok {
		t.Fatal("extra row")
	}
	if len(res.Notifications) != 1 || res.Notifications[0].Code != DeprecatedSyntax {
		t.Fatalf("note %+v", res.Notifications)
	}
}

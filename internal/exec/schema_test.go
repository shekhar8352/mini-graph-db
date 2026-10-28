package exec

import (
	"context"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

func TestIndexConstraintShowAndSeek(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Person {name: "Ada", age: 36}), (b:Person {name: "Bea", age: 22}), (c:Person {name: "Cy", age: 41})`, Options{})
	mustRun(t, g, `CREATE INDEX person_age FOR (n:Person) ON (n.age)`, Options{})
	mustRun(t, g, `CREATE INDEX person_name FOR (n:Person) ON (n.name)`, Options{})
	mustRun(t, g, `ANALYZE`, Options{})

	cols, rows := mustRun(t, g, `SHOW INDEXES`, Options{})
	if len(cols) != 5 || len(rows) != 2 {
		t.Fatalf("indexes %v %d", cols, len(rows))
	}
	if str(rows[0][0]) != "person_age" || str(rows[0][1]) != "node" || str(rows[0][4]) != "ONLINE" {
		t.Fatalf("row0 %#v", rows[0])
	}

	plan := explain(t, g, `MATCH (n:Person) WHERE n.age >= 30 AND n.age < 40 RETURN n.name AS name`)
	if !strings.Contains(plan, "IndexSeek") {
		t.Fatalf("range plan %q", plan)
	}
	_, rows = mustRun(t, g, `MATCH (n:Person) WHERE n.age >= 30 AND n.age < 40 RETURN n.name AS name`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "Ada" {
		t.Fatalf("range rows %#v", rows)
	}

	plan = explain(t, g, `MATCH (n:Person) WHERE n.name STARTS WITH "B" RETURN n.name AS name`)
	if !strings.Contains(plan, "IndexSeek") {
		t.Fatalf("starts plan %q", plan)
	}
	_, rows = mustRun(t, g, `MATCH (n:Person) WHERE n.name STARTS WITH "B" RETURN n.name AS name`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "Bea" {
		t.Fatalf("starts rows %#v", rows)
	}

	mustRun(t, g, `CREATE CONSTRAINT person_age_type FOR (n:Person) REQUIRE n.age IS :: INT`, Options{})
	_, err := Run(context.Background(), g, `CREATE (n:Person {name: "Dot", age: "x"})`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("type %v", err)
	}
	mustRun(t, g, `CREATE CONSTRAINT person_name_exists FOR (n:Person) REQUIRE n.name IS NOT NULL`, Options{})
	_, err = Run(context.Background(), g, `CREATE (n:Person {age: 1})`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("exists %v", err)
	}

	cols, rows = mustRun(t, g, `SHOW CONSTRAINTS`, Options{})
	if len(cols) != 7 || len(rows) != 2 {
		t.Fatalf("constraints %v %d", cols, len(rows))
	}

	_, err = Run(context.Background(), g, `CREATE INDEX person_age FOR (n:Person) ON (n.age)`, Options{})
	if gerr.CodeOf(err) != gerr.AlreadyExists {
		t.Fatalf("dup index %v", err)
	}
	_, err = Run(context.Background(), g, `DROP INDEX missing`, Options{})
	if gerr.CodeOf(err) != gerr.NotFound {
		t.Fatalf("drop missing %v", err)
	}
}

func TestLimitPushdownAndJoinOrder(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Rare {n: 1})`, Options{})
	for i := 0; i < 8; i++ {
		mustRun(t, g, `CREATE (c:Common {n: 1})`, Options{})
	}
	mustRun(t, g, `ANALYZE`, Options{})
	plan := explain(t, g, `MATCH (a:Rare), (b:Common) RETURN a, b`)
	rare := strings.Index(plan, "LabelScan((a:Rare))")
	common := strings.Index(plan, "LabelScan((b:Common))")
	if rare < 0 || common < 0 || rare > common {
		t.Fatalf("join order %q", plan)
	}
	plan = explain(t, g, `MATCH (a) RETURN a.name AS name LIMIT 3`)
	if !strings.Contains(plan, "Project(a.name AS name)\n  Limit(3)") {
		t.Fatalf("limit %q", plan)
	}
}

func TestIndexOrderBySkipSort(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Person {name: "Ada"}), (b:Person {name: "Bea"})`, Options{})
	mustRun(t, g, `CREATE INDEX person_name FOR (n:Person) ON (n.name)`, Options{})
	plan := explain(t, g, `MATCH (a:Person) RETURN a.name AS name ORDER BY a.name`)
	if strings.Contains(plan, "Sort") || !strings.Contains(plan, "IndexSeek") {
		t.Fatalf("order %q", plan)
	}
}

func TestIndexThenMatchSameScript(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Person {age: 21})`, Options{})
	_, rows := mustRun(t, g, `CREATE INDEX person_age FOR (n:Person) ON (n.age); MATCH (n:Person) WHERE n.age >= 18 RETURN n.age AS age`, Options{})
	if len(rows) != 1 {
		t.Fatalf("same script rows %#v", rows)
	}
	plan := explain(t, g, `MATCH (n:Person) WHERE n.age >= 18 RETURN n.age AS age`)
	if !strings.Contains(plan, "IndexSeek") {
		t.Fatalf("after script %q", plan)
	}
}

func TestIndexDDLRollback(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE (a:Person {name: "Ada"})`, Options{})
	mustRun(t, g, `BEGIN`, Options{})
	mustRun(t, g, `CREATE INDEX person_name FOR (n:Person) ON (n.name)`, Options{})
	mustRun(t, g, `ROLLBACK`, Options{})
	_, rows := mustRun(t, g, `SHOW INDEXES`, Options{})
	if len(rows) != 0 {
		t.Fatalf("rolled back %#v", rows)
	}
}

func explain(t *testing.T, g *graph.Graph, src string) string {
	t.Helper()
	res, err := Run(context.Background(), g, "EXPLAIN "+src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	all := res.All()
	if len(all) != 1 {
		t.Fatalf("explain rows %#v", all)
	}
	s, _ := all[0][0].StringValue()
	return s
}

func TestUniqueConstraintStillConflicts(t *testing.T) {
	g := graph.New()
	mustRun(t, g, `CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE`, Options{})
	mustRun(t, g, `CREATE (a:Person {email: "a@b.c"})`, Options{})
	_, err := Run(context.Background(), g, `CREATE (b:Person {email: "a@b.c"})`, Options{})
	if gerr.CodeOf(err) != gerr.ConstraintViolation {
		t.Fatalf("unique %v", err)
	}
	_, rows := mustRun(t, g, `SHOW INDEXES`, Options{})
	if len(rows) != 1 || str(rows[0][0]) != "person_email_unique" {
		t.Fatalf("backing %#v", rows)
	}
}

package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestShapes(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{
			`MATCH (a:Person {name: "Alice"}) RETURN a.name AS name`,
			"Project(a.name AS name)\n  IndexSeek((a:Person {name: \"Alice\"}))\n",
		},
		{
			`MATCH (a) RETURN a`,
			"Project(a)\n  NodeScan((a))\n",
		},
		{
			`MATCH (a:Person), (b:City) WHERE a.city = b.name RETURN a.name, b.name`,
			"Project(a.name, b.name)\n  Filter(a.city = b.name)\n    Apply\n      LabelScan((a:Person))\n      LabelScan((b:City))\n",
		},
		{
			`MATCH (a:Person) OPTIONAL MATCH (a)-[:LIVES_IN]->(c:City) RETURN a.name AS name, c.name AS city`,
			"Project(a.name AS name, c.name AS city)\n  Apply\n    LabelScan((a:Person))\n    Optional\n      Expand((a)-[:LIVES_IN]->(c:City))\n",
		},
		{
			`MATCH (a), (b) WHERE id(a) = $from AND id(b) = $to MATCH p = shortestPath((a)-[*]->(b)) RETURN p`,
			"Project(p)\n  Apply\n    Filter(id(a) = $`from` AND id(b) = $`to`)\n      Apply\n        NodeScan((a))\n        NodeScan((b))\n    ShortestPath(p = shortestPath((a)-[*]->(b)))\n",
		},
		{
			`UNWIND [1, 2] AS x RETURN x`,
			"Project(x)\n  Unwind([1, 2] AS x)\n",
		},
		{
			`MATCH (a:Person) RETURN a.name AS name UNION MATCH (c:City) RETURN c.name AS name`,
			"Union\n  Project(a.name AS name)\n    LabelScan((a:Person))\n  Project(c.name AS name)\n    LabelScan((c:City))\n",
		},
		{
			`MATCH (a:Person) RETURN a.name AS name UNION ALL MATCH (c:City) RETURN c.name AS name`,
			"Union(ALL)\n  Project(a.name AS name)\n    LabelScan((a:Person))\n  Project(c.name AS name)\n    LabelScan((c:City))\n",
		},
		{
			`CREATE DATABASE analytics`,
			"CreateDatabase(analytics)\n",
		},
		{
			`BEGIN READ ONLY`,
			"Begin(READ ONLY)\n",
		},
		{
			`DENY WRITE ON DATABASE default TO analyst`,
			"Deny(WRITE ON DATABASE default TO analyst)\n",
		},
		{
			`MATCH (a:Person)-[k:KNOWS*1..3]->(b:Person) WHERE b.age >= 18 AND NOT b.name STARTS WITH "Z" AND ALL (e IN k WHERE e.since IS NOT NULL) RETURN b.name AS name, size(k) AS hops, collect(DISTINCT b.city) AS cities ORDER BY hops DESC, name SKIP 10 LIMIT 20`,
			"Limit(20)\n  Skip(10)\n    Sort(hops DESC, name ASC)\n      Aggregate(b.name AS name, size(k) AS hops, collect(DISTINCT b.city) AS cities)\n        Filter(b.age >= 18 AND NOT b.name STARTS WITH \"Z\" AND ALL(e IN k WHERE e.since IS NOT NULL))\n          Expand((a)-[k:KNOWS*..3]->(b:Person))\n            LabelScan((a:Person))\n",
		},
		{
			`MATCH (a:Person) WITH a.name AS name, count(*) AS n WHERE n > 1 RETURN name, n`,
			"Project(name, n)\n  Filter(n > 1)\n    Aggregate(a.name AS name, count(*) AS n)\n      LabelScan((a:Person))\n",
		},
		{
			`MERGE (a:Person {email: $email}) ON CREATE SET a.created = datetime("2026-10-07T00:00:00Z") ON MATCH SET a.seen = a.seen + 1 RETURN id(a)`,
			"Project(id(a))\n  Merge((a:Person {email: $email}); on create SetProps(a.created = datetime(\"2026-10-07T00:00:00Z\")); on match SetProps(a.seen = a.seen + 1))\n",
		},
		{
			`MATCH (a:Person)-[:KNOWS]->(b)-[:WORKS]->(c) RETURN c`,
			"Project(c)\n  Expand((b)-[:WORKS]->(c))\n    Expand((a)-[:KNOWS]->(b))\n      LabelScan((a:Person))\n",
		},
		{
			`MATCH (a) SET a.age = 1, a:Flagged RETURN a`,
			"Project(a)\n  SetLabels(a:Flagged)\n    SetProps(a.age = 1)\n      NodeScan((a))\n",
		},
		{
			`MATCH (a) DETACH DELETE a`,
			"Delete(DETACH a)\n  NodeScan((a))\n",
		},
		{
			`CALL db.labels() YIELD label WHERE label STARTS WITH "P" RETURN label`,
			"Project(label)\n  Filter(label STARTS WITH \"P\")\n    Project(label)\n      Call(db.labels())\n",
		},
		{
			`MATCH (a) RETURN DISTINCT a.name`,
			"Distinct\n  Project(a.name)\n    NodeScan((a))\n",
		},
		{
			`EXPLAIN MATCH (a:Person) RETURN a`,
			"Project(a)\n  LabelScan((a:Person))\n",
		},
		{
			`CREATE (a:Person)-[:KNOWS]->(b:Person) RETURN a`,
			"Project(a)\n  Create((a:Person)-[:KNOWS]->(b:Person))\n",
		},
		{
			`MATCH (a) REMOVE a.age, a:Person RETURN a`,
			"Project(a)\n  Remove(a.age, a:Person)\n    NodeScan((a))\n",
		},
		{
			`CREATE DATABASE a; USE a`,
			"CreateDatabase(a)\n\nUse(a)\n",
		},
	}
	for _, tc := range cases {
		p, err := Build(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got := p.String(); got != tc.want {
			t.Fatalf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

func TestSpecExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "spec", "examples")
	matches, err := filepath.Glob(filepath.Join(dir, "*.gql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 60 {
		t.Fatalf("corpus has %d scripts", len(matches))
	}
	reject := map[string]gerr.Code{
		"e51-merge-varlen.gql":    gerr.Semantic,
		"e52-aggregate-where.gql": gerr.Semantic,
		"e53-begin-read.gql":      gerr.Syntax,
		"e55-drop-database.gql":   gerr.Semantic,
	}
	for _, path := range matches {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			p, err := Build(string(src))
			if code, ok := reject[name]; ok {
				if gerr.CodeOf(err) != code {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			text := p.String()
			if text == "" || p.String() != text {
				t.Fatal("unstable plan")
			}
			for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				if line == "" {
					continue
				}
				trim := strings.TrimLeft(line, " ")
				indent := len(line) - len(trim)
				if indent%2 != 0 {
					t.Fatalf("bad indent %q", line)
				}
				op := trim
				if i := strings.IndexByte(op, '('); i >= 0 {
					op = op[:i]
				}
				if !knownOp(op) {
					t.Fatalf("unknown operator %q in %q", op, line)
				}
			}
		})
	}
}

func knownOp(name string) bool {
	switch name {
	case "NodeScan", "LabelScan", "IndexSeek", "Expand", "Filter", "Project",
		"Aggregate", "Sort", "Limit", "Skip", "Distinct", "Union", "Unwind",
		"Optional", "Apply", "Create", "Merge", "SetProps", "SetLabels",
		"Remove", "Delete", "ShortestPath", "Call", "Begin", "Commit",
		"Rollback", "Vacuum", "Analyze", "Use", "Terminate", "Show",
		"CreateIndex", "DropIndex", "CreateConstraint", "DropConstraint",
		"CreateDatabase", "DropDatabase", "CreateUser", "AlterUser",
		"DropUser", "CreateRole", "DropRole", "GrantRole", "RevokeRole",
		"Grant", "Revoke", "Deny", "CreateToken", "RevokeToken":
		return true
	default:
		return false
	}
}

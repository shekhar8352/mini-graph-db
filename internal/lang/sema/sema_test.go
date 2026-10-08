package sema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func TestSpecExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "spec", "examples")
	matches, err := filepath.Glob(filepath.Join(dir, "*.gql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 60 {
		t.Fatalf("corpus has %d scripts", len(matches))
	}
	want := map[string]gerr.Code{
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
			err = Check(string(src))
			code, reject := want[name]
			if !reject {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if gerr.CodeOf(err) != code {
				t.Fatalf("got %v", err)
			}
			if !strings.Contains(err.Error(), " at ") {
				t.Fatal(err)
			}
		})
	}
}

func TestRules(t *testing.T) {
	ok := []string{
		`MATCH (a)-[]->(a) RETURN a`,
		`MATCH (a) WITH a AS a WHERE a IS NOT NULL RETURN a`,
		`MATCH (n) RETURN n.name, count(*) ORDER BY n.name`,
		`MATCH (n) RETURN count(*) AS n ORDER BY count(*)`,
		`MATCH (a)-[k*1..2]->(b) RETURN size(k)`,
		`MATCH (a), (b) MATCH p = shortestPath((a)-[*]->(b)) RETURN p`,
		`RETURN 1 + 2 * 3`,
		`RETURN date("2025-01-31") + duration("P1M")`,
		`RETURN toString(1)`,
		`CALL db.labels() WHERE label STARTS WITH "A"`,
		`CREATE DATABASE analytics`,
		`USE default`,
		`DENY WRITE ON DATABASE default TO analyst`,
	}
	for _, src := range ok {
		if err := Check(src); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
	bad := []struct {
		src  string
		code gerr.Code
		msg  string
	}{
		{`RETURN a`, gerr.Semantic, "unbound variable a at 1:8"},
		{`RETURN 1 AS a, 2 AS a`, gerr.Semantic, "already bound"},
		{`RETURN 1 + "a"`, gerr.InvalidArgument, "invalid +"},
		{`RETURN duration("P1D") + date("2025-01-01")`, gerr.InvalidArgument, "invalid +"},
		{`RETURN 1[0]`, gerr.InvalidArgument, "index on"},
		{`MATCH (a) DELETE 1`, gerr.InvalidArgument, "DELETE expects"},
		{`RETURN toUpper(1)`, gerr.InvalidArgument, "argument"},
		{`RETURN noSuch(1)`, gerr.InvalidArgument, "unknown function"},
		{`RETURN size(1, 2)`, gerr.Semantic, "wrong number"},
		{`RETURN size(DISTINCT 1)`, gerr.Semantic, "DISTINCT"},
		{`MATCH (a)-[*0]->(b) RETURN a`, gerr.Semantic, "hop lower bound"},
		{`MATCH (a)-[*3..1]->(b) RETURN a`, gerr.Semantic, "upper bound"},
		{`CREATE (a)-[]->(b) RETURN a`, gerr.Semantic, "one type"},
		{`CREATE (a)-[:A|B]->(b) RETURN a`, gerr.Semantic, "one type"},
		{`MATCH (a)-[k*]->(b) RETURN k.since`, gerr.Semantic, "property on a list"},
		{`MATCH (n) RETURN n SKIP n`, gerr.Semantic, "row variable"},
		{`MATCH (n) RETURN count(*) + n.age`, gerr.Semantic, "grouping key"},
		{`MATCH (n) RETURN count(count(n))`, gerr.Semantic, "nested aggregate"},
		{`MATCH (n) RETURN n ORDER BY missing`, gerr.Semantic, "unbound variable"},
		{`RETURN {a: 1, a: 2}`, gerr.Semantic, "duplicate map key"},
		{`MATCH (a) UNWIND [1] AS a RETURN a`, gerr.Semantic, "already bound"},
		{`WITH 1 AS a`, gerr.Semantic, "WITH is the last"},
		{`MATCH (n) RETURN n MATCH (m) RETURN m`, gerr.Semantic, "RETURN is not the last"},
		{`MATCH (n)`, gerr.Semantic, "no RETURN"},
		{`CALL db.labels() RETURN label`, gerr.Semantic, "YIELD"},
		{`CALL db.labels() YIELD nope RETURN nope`, gerr.Semantic, "unknown yield"},
		{`CALL missing.proc()`, gerr.Semantic, "unknown procedure"},
		{`RETURN 1 AS a UNION RETURN 1 AS b`, gerr.Semantic, "UNION columns"},
		{`MATCH (a) MATCH p = shortestPath((b)-[*]->(a)) RETURN p`, gerr.Semantic, "not bound"},
		{`MATCH (a), (b) MATCH p = shortestPath((a)-[{k: 1}]->(b)) RETURN p`, gerr.Semantic, "property map"},
		{`CREATE INDEX i FOR (n:Person) ON (m.name)`, gerr.Semantic, "does not match"},
		{`CREATE DATABASE Default`, gerr.Semantic, "database name"},
		{`CREATE DATABASE system`, gerr.Semantic, "reserved"},
	}
	for _, tc := range bad {
		err := Check(tc.src)
		if gerr.CodeOf(err) != tc.code {
			t.Fatalf("%s\n got %v", tc.src, err)
		}
		if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%s\n got %v\nwant %q", tc.src, err, tc.msg)
		}
	}
}

func TestSelfLoopAndParams(t *testing.T) {
	if err := Check(`MATCH (a)-[]->(a) RETURN a`); err != nil {
		t.Fatal(err)
	}
	src := `MATCH (n) WHERE n.name = $name RETURN n`
	if err := Check(src); err != nil {
		t.Fatal(err)
	}
	err := CheckParams(src, map[string]Type{})
	if gerr.CodeOf(err) != gerr.InvalidArgument || !strings.Contains(err.Error(), "missing parameter $name") {
		t.Fatal(err)
	}
	if err := CheckParams(src, map[string]Type{"name": String}); err != nil {
		t.Fatal(err)
	}
	err = CheckParams(`RETURN id($n)`, map[string]Type{"n": Int})
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatal(err)
	}
}

func TestDropConfirmMessage(t *testing.T) {
	err := Check(`DROP DATABASE analytics`)
	if !strings.Contains(err.Error(), "CONFIRM") {
		t.Fatal(err)
	}
}

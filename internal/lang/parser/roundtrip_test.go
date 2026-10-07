package parser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
)

func TestRoundTrip(t *testing.T) {
	for i, src := range corpus {
		t.Run(strings.TrimSpace(firstLine(src)), func(t *testing.T) {
			first, err := Parse(src)
			if err != nil {
				t.Fatalf("parse %d: %v\n%s", i, err, src)
			}
			formatted := ast.Format(first)
			second, err := Parse(formatted)
			if err != nil {
				t.Fatalf("reparse %q: %v", formatted, err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("round trip\nsrc:\n%s\nformat:\n%s\nagain:\n%s", src, formatted, ast.Format(second))
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ in, out string }{
		{`RETURN 0x2A`, `RETURN 42`},
		{`RETURN a != b`, `RETURN a <> b`},
		{`RETURN a <> b`, `RETURN a <> b`},
		{`MATCH (a)-[k*1..]->(b) RETURN k`, `MATCH (a)-[k*]->(b) RETURN k`},
		{`MATCH (a)-[k*]->(b) RETURN k`, `MATCH (a)-[k*]->(b) RETURN k`},
		{`MATCH (a)-[k*1..3]->(b) RETURN k`, `MATCH (a)-[k*..3]->(b) RETURN k`},
		{`MATCH (a)-[k*2..2]->(b) RETURN k`, `MATCH (a)-[k*2]->(b) RETURN k`},
		{`RETURN a UNION DISTINCT RETURN b`, `RETURN a UNION RETURN b`},
		{`RETURN a UNION ALL RETURN b`, `RETURN a UNION ALL RETURN b`},
		{`RETURN n ORDER BY n ASC`, `RETURN n ORDER BY n`},
		{`match (n) return n`, `MATCH (n) RETURN n`},
		{`RETURN -2^2`, `RETURN -2 ^ 2`},
		{`RETURN (-2)^2`, `RETURN (-2) ^ 2`},
		{`MATCH (a)- -(b) RETURN a`, `MATCH (a)- -(b) RETURN a`},
		{`MATCH (a)-->(b) RETURN a`, `MATCH (a)-->(b) RETURN a`},
		{`MATCH (a)<--(b) RETURN a`, `MATCH (a)<--(b) RETURN a`},
	}
	for _, tc := range cases {
		tree, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		got := ast.Format(tree)
		if got != tc.out {
			t.Fatalf("%s\n got %s\nwant %s", tc.in, got, tc.out)
		}
		again, err := Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tree, again) {
			t.Fatalf("canonical not stable: %s", tc.in)
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []string{
		`BEGIN READ`,
		`RETURN a < b < c`,
		`MATCH (n`,
		`RETURN "\q"`,
		`(match)`,
	}
	for _, src := range cases {
		_, err := Parse(src)
		if err == nil {
			t.Fatalf("accepted %q", src)
		}
		if gerr.CodeOf(err) != gerr.Syntax {
			t.Fatalf("%q code %s", src, gerr.CodeOf(err))
		}
		if !strings.Contains(err.Error(), " at ") {
			t.Fatalf("%q message %v", src, err)
		}
	}
}

func TestEmptyScript(t *testing.T) {
	for _, src := range []string{"", "   ", ";", ";;", "-- only\n", "/* c */"} {
		tree, err := Parse(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(tree.Stmts) != 0 {
			t.Fatalf("%q statements %d", src, len(tree.Stmts))
		}
		if ast.Format(tree) != "" {
			t.Fatalf("format %q", ast.Format(tree))
		}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

var corpus = []string{
	`MATCH (a:Person {name: "Alice"})
RETURN a.name AS name`,

	`MATCH (a:Person)-[k:KNOWS*1..3]->(b:Person)
WHERE b.age >= 18 AND NOT b.name STARTS WITH "Z"
  AND ALL (e IN k WHERE e.since IS NOT NULL)
RETURN b.name AS name, size(k) AS hops, collect(DISTINCT b.city) AS cities
ORDER BY hops DESC, name
SKIP 10 LIMIT 20`,

	`MATCH (a), (b)
WHERE id(a) = $from AND id(b) = $to
MATCH p = shortestPath((a)-[*]->(b))
RETURN p`,

	`MATCH (a) WHERE id(a) = 1
RETURN neighbors(a, 2)`,

	`MATCH (a:Person)
OPTIONAL MATCH (a)-[:LIVES_IN]->(c:City)
RETURN a.name AS name, c.name AS city`,

	`UNWIND $list AS x
RETURN x ORDER BY x`,

	`MATCH (a:Person)
WITH a.name AS name, count(*) AS n
WHERE n > 1
RETURN name, n`,

	`MATCH (a:Person) RETURN a.name AS name
UNION
MATCH (c:City) RETURN c.name AS name`,

	`CREATE (a:Person {name: "Alice", tags: ["x", "y"]})-[:KNOWS {since: 2020}]->(b:Person {name: "Bob"})
RETURN id(a), id(b)`,

	`MERGE (a:Person {email: $email})
  ON CREATE SET a.created = datetime("2026-10-07T00:00:00Z")
  ON MATCH SET a.seen = coalesce(a.seen, 0) + 1
RETURN id(a)`,

	`MATCH (a:Person)
WHERE a.age < 0
SET a.age = 0, a:Flagged
REMOVE a.temp
RETURN a`,

	`MATCH (a:Person {name: "Bob"})
DETACH DELETE a`,

	`MATCH (a:Person {name: "Bob"})
DELETE a`,

	`BEGIN;
MATCH (a:Person) WHERE a.name = "Alice" SET a.age = 31;
COMMIT`,

	`BEGIN READ ONLY;
MATCH (a:Person) RETURN a.name;
COMMIT`,

	`CREATE INDEX person_name FOR (n:Person) ON (n.name);
CREATE INDEX knows_since FOR ()-[r:KNOWS]-() ON (r.since);
CREATE CONSTRAINT person_email_unique FOR (n:Person) REQUIRE n.email IS UNIQUE;
CREATE CONSTRAINT person_name_exists FOR (n:Person) REQUIRE n.name IS NOT NULL;
CREATE CONSTRAINT person_age_type FOR (n:Person) REQUIRE n.age IS :: INT;
DROP INDEX person_name;
DROP CONSTRAINT person_email_unique`,

	`SHOW INDEXES;
SHOW CONSTRAINTS;
SHOW LABELS;
SHOW EDGE TYPES;
SHOW PROPERTY KEYS;
SHOW STATS`,

	`CREATE DATABASE analytics;
SHOW DATABASES;
USE analytics;
CREATE USER alice SET PASSWORD "s3cret" CHANGE REQUIRED;
ALTER USER alice SET PASSWORD "better";
CREATE ROLE analyst;
GRANT READ ON DATABASE analytics TO analyst;
GRANT ROLE analyst TO alice;
DENY WRITE ON DATABASE default TO analyst;
SHOW USERS;
SHOW ROLES;
SHOW PRIVILEGES FOR alice;
REVOKE ROLE analyst FROM alice;
REVOKE WRITE ON DATABASE analytics FROM analyst;
DROP USER alice;
DROP ROLE analyst;
DROP DATABASE analytics CONFIRM`,

	`CREATE TOKEN FOR USER svc_app EXPIRES IN duration("P90D");
REVOKE TOKEN 1`,

	`EXPLAIN MATCH (n:Person) RETURN n.name;
PROFILE MATCH (n:Person) RETURN n.name;
VACUUM;
SHOW TRANSACTIONS;
TERMINATE TRANSACTION 7;
ANALYZE`,

	"MATCH (n:Person)\nRETURN n.`match` AS `return`",

	`-- line comment
MATCH (n:Person) /* block */ RETURN n.name; /* trailing */`,

	`MATCH (n) WHERE n.code = 0x2A OR n.score = 1.5e1 RETURN n`,

	`WITH [1, 2, 3, null] AS xs
RETURN [x IN xs WHERE x > 1 | x * 10] AS tens,
       ANY (x IN xs WHERE x = 2) AS hasTwo,
       ALL (x IN xs WHERE x IS NULL OR x > 0) AS ok`,

	`RETURN REDUCE (s = 0, x IN [1, 2, 3] | s + x) AS sum`,

	`MATCH (n:Person)
RETURN CASE WHEN n.age IS NULL THEN "missing" WHEN n.age >= 18 THEN "adult" ELSE "minor" END AS band`,

	`RETURN CASE n.age WHEN 0 THEN "zero" ELSE "other" END`,

	`MATCH (n:Person)
WHERE n.name IN ["Ada", "Bob"]
  AND n.email STARTS WITH "a"
  AND n.email ENDS WITH "b"
  AND n.note CONTAINS "x"
  AND n.age IS :: INT
  AND n.nick IS NOT :: STRING
RETURN n`,

	`RETURN [1, 2, 3, 4][1..3] AS mid, [1, 2, 3][-1] AS last, xs[1..] AS tail`,

	`RETURN coalesce(null, 1, 2) AS first, typeOf(1.0) AS k`,

	`RETURN toUpper("Ada") AS u, split("a,b", ",") AS parts,
       substring("hello", 1, 3) AS sub, size("héllo") AS chars,
       tail(reverse(range(1, 3))) AS xs`,

	`RETURN abs(-3) AS a, round(1.5) AS r, sign(-0.0) AS s, sqrt(4) AS q`,

	`RETURN date("2025-01-31") + duration("P1M") AS d,
       datetime("2025-01-31T00:00:00+05:30") + duration("P1D") AS t,
       year(date("2024-02-29")) AS y`,

	`MATCH (a), (b)
WHERE id(a) = 1 AND id(b) = 2
WITH shortestPath(a, b) AS p
RETURN length(p) AS hops, nodes(p) AS ns, relationships(p) AS es`,

	`MATCH (n) WHERE id(n) = 1
RETURN degree(n, "both") AS d, keys(n) AS ks, properties(n) AS props`,

	`MATCH (a:Person)-[:KNOWS]->(b)
RETURN count(DISTINCT b.city) AS cities`,

	`MATCH (a:Person)
RETURN labels(a) AS labs, avg(a.age) AS mean, min(a.age) AS youngest
ORDER BY mean DESC NULLS LAST`,

	`MATCH (n) WHERE id(n) = 1
SET n = {name: "Ada"}, n += {city: "Paris"}
RETURN n.name, n.city`,

	`MATCH (n) WHERE id(n) = 1
SET n.temp = null
RETURN n.temp IS NULL AS gone`,

	`UNWIND [1, 2] AS idv
MATCH (n) WHERE id(n) = idv
RETURN n.name`,

	`MATCH (a:Person), (b:City)
WHERE a.city = b.name
RETURN a.name, b.name`,

	`MATCH (a)<-[:KNOWS]-(b) RETURN id(a), id(b);
MATCH (a)-[:KNOWS]-(b) RETURN id(b)`,

	`MATCH (n:Person:Employee|Contractor) RETURN n`,

	`CALL db.labels() YIELD label AS name
WHERE name STARTS WITH "P"
RETURN name ORDER BY name`,

	`CREATE (a:Person {name: "Cara"})
WITH a
MATCH (b:Person) WHERE id(b) = id(a)
RETURN b.name`,

	`MATCH (n:Person)
RETURN n.name ORDER BY n.name SKIP $offset LIMIT $limit`,

	`MATCH (n:Person)
RETURN percentileDisc(n.age, 0.5) AS median, stdev(n.age) AS spread`,

	`RETURN bytes("aGVsbG8=") AS raw, keys({b: 1, a: 2, "c d": 3}) AS ks`,

	`RETURN true XOR false AS t, null XOR true AS u`,

	`RETURN false AND (1 / 0 = 1) AS safe`,

	`BEGIN READ ONLY;
CREATE (:Person {name: "nope"})`,

	`MERGE (a)-[:KNOWS*1..2]->(b) RETURN a`,

	`MATCH (n:Person) WHERE count(n) > 1 RETURN n`,

	`MATCH (n) WHERE n.name = $name RETURN n`,

	`DROP DATABASE analytics`,

	`MATCH (a)-[e:KNOWS]->(b)-[f:KNOWS]->(a)
WHERE id(e) <> id(f)
RETURN e, f`,

	`MATCH (a), (b)
WHERE id(a) = $from AND id(b) = $to
RETURN allShortestPaths(a, b) AS paths`,

	`CREATE CONSTRAINT city_name_exists FOR (n:City) REQUIRE n.name IS NOT NULL;
CREATE CONSTRAINT city_pop_type FOR (n:City) REQUIRE n.pop IS :: INT;
MATCH (n:City) WHERE n.name IS NOT NULL RETURN n.name`,

	`MATCH p = (a:Person)-[:KNOWS*]->(b:Person)
RETURN relationships(p) AS edges, length(p) AS hops`,

	`CREATE (:Person {name: "Diya"});
MATCH (n:Person {name: "Diya"}) RETURN id(n)`,

	`MATCH p = allShortestPaths((a)-[r:KNOWS|WORKS*2..4 {since: 1}]-(b)) RETURN p`,

	"MATCH (`match`) RETURN `match`.name, $`from`",

	`RETURN NOT NOT a, a OR b AND c, (a OR b) AND c, 2 ^ 3 ^ 4, (2 ^ 3) ^ 4, 1 * -2`,

	`RETURN [] AS empty, {} AS none, [x IN xs | x] AS ids, [x IN xs WHERE x] AS kept`,

	`RETURN a % 2, a / b, +a, NAN, INF, TRUE, FALSE, NULL`,

	`GRANT ADMIN ON DATABASE analytics TO owner;
REVOKE READ ON DATABASE analytics FROM guest`,

	`RETURN "a\n\t\"\\" AS s`,

	`MATCH (a)-[{since: 1}]->(b) RETURN a`,

	`RETURN n ORDER BY n NULLS FIRST, m DESC NULLS LAST`,

	`;; MATCH (n) RETURN DISTINCT *;`,
}

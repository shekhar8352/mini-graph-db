package expr

import (
	"math"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/parser"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func mustExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	s, err := parser.Parse("RETURN " + src)
	if err != nil {
		t.Fatal(err)
	}
	q := s.Stmts[0].(*ast.Query)
	ret := q.Parts[0].Clauses[0].(*ast.Return)
	return ret.Body.Items[0].Expr
}

func evalSrc(t *testing.T, src string, env *Env) value.Value {
	t.Helper()
	if env == nil {
		env = &Env{}
	}
	v, err := Eval(mustExpr(t, src), env)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return v
}

func wantEq(t *testing.T, src string, env *Env, want value.Value) {
	t.Helper()
	got := evalSrc(t, src, env)
	if !value.Equal(got, want) {
		t.Fatalf("%s\n got %s\nwant %s", src, got, want)
	}
}

func wantErr(t *testing.T, src string, env *Env, code gerr.Code) {
	t.Helper()
	if env == nil {
		env = &Env{}
	}
	_, err := Eval(mustExpr(t, src), env)
	if gerr.CodeOf(err) != code {
		t.Fatalf("%s: %v", src, err)
	}
}

func TestLogic(t *testing.T) {
	wantEq(t, `false AND (1 / 0 = 1)`, nil, value.Bool(false))
	wantEq(t, `true OR (1 / 0 = 1)`, nil, value.Bool(true))
	wantEq(t, `null AND false`, nil, value.Bool(false))
	wantEq(t, `null AND true`, nil, value.Null())
	wantEq(t, `null OR false`, nil, value.Null())
	wantEq(t, `null OR true`, nil, value.Bool(true))
	wantEq(t, `NOT null`, nil, value.Null())
	wantEq(t, `true XOR false`, nil, value.Bool(true))
	wantEq(t, `null XOR true`, nil, value.Null())
	wantEq(t, `true = false`, nil, value.Bool(false))
	wantEq(t, `true < false`, nil, value.Null())
	wantEq(t, `1 = 1.0`, nil, value.Bool(true))
	wantEq(t, `1 < "a"`, nil, value.Null())
	wantEq(t, `1 <> null`, nil, value.Null())
	wantErr(t, `null AND (1 / 0)`, nil, gerr.InvalidArgument)
	wantErr(t, `1 AND true`, nil, gerr.InvalidArgument)
}

func TestArithAndPredicates(t *testing.T) {
	wantEq(t, `1 + 2 * 3`, nil, value.Int(7))
	wantEq(t, `1 / 2`, nil, value.Int(0))
	wantEq(t, `5 / 2.0`, nil, value.Float(2.5))
	wantEq(t, `-5 % 2`, nil, value.Int(-1))
	wantEq(t, `"a" + "b"`, nil, value.String("ab"))
	wantEq(t, `[1] + [2, 3]`, nil, value.List(value.Int(1), value.Int(2), value.Int(3)))
	wantEq(t, `null + "a"`, nil, value.Null())
	wantEq(t, `2 ^ 3`, nil, value.Float(8))
	wantEq(t, `1 IN [1, "a"]`, nil, value.Bool(true))
	wantEq(t, `1 IN ["a"]`, nil, value.Null())
	wantEq(t, `1 IN null`, nil, value.Null())
	wantEq(t, `3 IN [1, 2]`, nil, value.Bool(false))
	wantEq(t, `"ab" STARTS WITH "a"`, nil, value.Bool(true))
	wantEq(t, `"" STARTS WITH ""`, nil, value.Bool(true))
	wantEq(t, `"ab" ENDS WITH "b"`, nil, value.Bool(true))
	wantEq(t, `"abc" CONTAINS "b"`, nil, value.Bool(true))
	wantEq(t, `null CONTAINS "a"`, nil, value.Null())
	wantEq(t, `1 IS NULL`, nil, value.Bool(false))
	wantEq(t, `null IS :: NULL`, nil, value.Bool(true))
	wantEq(t, `1 IS :: INT`, nil, value.Bool(true))
	wantEq(t, `1 IS NOT :: STRING`, nil, value.Bool(true))
	wantErr(t, `1 / 0`, nil, gerr.InvalidArgument)
	wantErr(t, `1.0 / 0.0`, nil, gerr.InvalidArgument)
	wantErr(t, `0 ^ -1`, nil, gerr.InvalidArgument)
	wantErr(t, `1 + "a"`, nil, gerr.InvalidArgument)
	wantErr(t, `1 % 0`, nil, gerr.InvalidArgument)
	wantErr(t, `"a" STARTS WITH 1`, nil, gerr.InvalidArgument)
	wantErr(t, `1 IN 1`, nil, gerr.InvalidArgument)

	env := &Env{}
	env.Set("n", value.Int(math.MinInt64))
	_, err := Eval(mustExpr(t, `-n`), env)
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatal(err)
	}
	env.Set("a", value.Int(math.MaxInt64))
	_, err = Eval(mustExpr(t, `a + 1`), env)
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatal(err)
	}
}

func TestTemporal(t *testing.T) {
	wantEq(t, `date("2025-01-31") + duration("P1M")`, nil, mustDate(t, "2025-02-28"))
	wantEq(t, `date("2025-03-31") + duration("-P1M")`, nil, mustDate(t, "2025-02-28"))
	wantEq(t, `date("2025-01-31") - date("2025-01-01")`, nil, value.Duration(0, 30, 0))
	wantEq(t, `duration("P1D") + duration("PT2H")`, nil, mustDur(t, "P1DT2H"))
	wantEq(t, `duration("P1DT2H") * 2`, nil, mustDur(t, "P2DT4H"))
	wantEq(t, `duration("PT1S") / 2`, nil, value.Duration(0, 0, int64(time.Second)/2))
	wantEq(t, `year(date("2024-02-29"))`, nil, value.Int(2024))
	wantEq(t, `dayOfWeek(date("2024-01-01"))`, nil, value.Int(1))
	wantEq(t, `quarter(date("2024-04-01"))`, nil, value.Int(2))
	wantEq(t, `hours(duration("P1DT2H"))`, nil, value.Int(2))
	wantEq(t, `seconds(duration("PT1.5S"))`, nil, value.Int(1))
	wantEq(t, `date(datetime("2025-01-31T00:00:00+05:30"))`, nil, mustDate(t, "2025-01-30"))
	wantEq(t, `hour(datetime("2025-01-31T00:00:00+05:30"))`, nil, value.Int(0))
	wantEq(t, `offsetSeconds(datetime("2025-01-31T00:00:00+05:30"))`, nil, value.Int(19800))
	wantEq(t, `offsetSeconds(datetime("2025-01-31T00:00:00"))`, nil, value.Null())
	wantErr(t, `date("2025-01-31") + duration("PT1S")`, nil, gerr.InvalidArgument)
	wantErr(t, `duration("P1D") * 1.5`, nil, gerr.InvalidArgument)
	wantErr(t, `duration("P1D") / 0`, nil, gerr.InvalidArgument)

	left := evalSrc(t, `datetime("2025-01-31T00:00:00+05:30") + duration("P1D")`, nil)
	inst, zone, off, ok := left.DateTimeValue()
	if !ok || !zone || off != 19800 {
		t.Fatalf("datetime+duration zone: %s", left)
	}
	if !inst.Equal(time.Date(2025, 1, 31, 18, 30, 0, 0, time.UTC)) {
		t.Fatal(inst)
	}
}

func TestFunctions(t *testing.T) {
	wantEq(t, `toUpper("Ada")`, nil, value.String("ADA"))
	wantEq(t, `trim("  a  ")`, nil, value.String("a"))
	wantEq(t, `split("a,b", ",")`, nil, value.List(value.String("a"), value.String("b")))
	wantEq(t, `split("", ",")`, nil, value.List(value.String("")))
	wantEq(t, `substring("hello", 1, 3)`, nil, value.String("ell"))
	wantEq(t, `substring("héllo", 1, 2)`, nil, value.String("él"))
	wantEq(t, `replace("a-a", "-", "")`, nil, value.String("aa"))
	wantEq(t, `size("héllo")`, nil, value.Int(5))
	wantEq(t, `toString(null)`, nil, value.Null())
	wantEq(t, `toString(1)`, nil, value.String("1"))
	wantEq(t, `abs(-3)`, nil, value.Int(3))
	wantEq(t, `ceil(1.2)`, nil, value.Float(2))
	wantEq(t, `floor(-1.2)`, nil, value.Float(-2))
	wantEq(t, `round(1.5)`, nil, value.Float(2))
	wantEq(t, `round(-1.5)`, nil, value.Float(-2))
	wantEq(t, `sign(-0.0)`, nil, value.Int(0))
	wantEq(t, `sign(NaN)`, nil, value.Null())
	wantEq(t, `sqrt(4)`, nil, value.Float(2))
	wantEq(t, `head([1, 2])`, nil, value.Int(1))
	wantEq(t, `head([])`, nil, value.Null())
	wantEq(t, `tail([1, 2])`, nil, value.List(value.Int(2)))
	wantEq(t, `tail([])`, nil, value.List())
	wantEq(t, `last([1, 2])`, nil, value.Int(2))
	wantEq(t, `reverse([1, 2])`, nil, value.List(value.Int(2), value.Int(1)))
	wantEq(t, `range(1, 3)`, nil, value.List(value.Int(1), value.Int(2), value.Int(3)))
	wantEq(t, `range(3, 1)`, nil, value.List())
	wantEq(t, `range(5, 1, -2)`, nil, value.List(value.Int(5), value.Int(3), value.Int(1)))
	wantEq(t, `keys({b: 1, a: 2})`, nil, value.List(value.String("a"), value.String("b")))
	wantEq(t, `values({b: 1, a: 2})`, nil, value.List(value.Int(2), value.Int(1)))
	wantEq(t, `bytes("aGVsbG8=")`, nil, value.Bytes([]byte("hello")))
	wantEq(t, `size(bytes("aGVsbG8="))`, nil, value.Int(5))
	wantEq(t, `toInt("12")`, nil, value.Int(12))
	wantEq(t, `toInt(1.9)`, nil, value.Int(1))
	wantEq(t, `toInt(-1.9)`, nil, value.Int(-1))
	wantEq(t, `toFloat("1.5")`, nil, value.Float(1.5))
	wantEq(t, `toBool("TRUE")`, nil, value.Bool(true))
	wantEq(t, `coalesce(null, 1, 1 / 0)`, nil, value.Int(1))
	wantEq(t, `typeOf(null)`, nil, value.String("null"))
	wantEq(t, `typeOf(1)`, nil, value.String("int"))
	wantErr(t, `split("a", "")`, nil, gerr.InvalidArgument)
	wantErr(t, `replace("a", "", "b")`, nil, gerr.InvalidArgument)
	wantErr(t, `substring("a", -1)`, nil, gerr.InvalidArgument)
	wantErr(t, `sqrt(-1)`, nil, gerr.InvalidArgument)
	wantErr(t, `toInt(true)`, nil, gerr.InvalidArgument)
	wantErr(t, `toUpper(1)`, nil, gerr.InvalidArgument)
	wantErr(t, `noSuch(1)`, nil, gerr.InvalidArgument)
	wantErr(t, `size(1, 2)`, nil, gerr.Semantic)
	wantErr(t, `count(*)`, nil, gerr.Semantic)

	env := &Env{Rand: func() float64 { return 0.25 }}
	wantEq(t, `rand()`, env, value.Float(0.25))
	fixed := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	env.Now = func() value.Value {
		v, err := value.DateTimeOffset(fixed, 0)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	got := evalSrc(t, `now()`, env)
	inst, zone, off, ok := got.DateTimeValue()
	if !ok || !zone || off != 0 || !inst.Equal(fixed) {
		t.Fatal(got)
	}
}

func TestCollections(t *testing.T) {
	wantEq(t, `[1, 2, 3][-1]`, nil, value.Int(3))
	wantEq(t, `[1, 2, 3, 4][1..3]`, nil, value.List(value.Int(2), value.Int(3)))
	wantEq(t, `[1, 2, 3][-2..]`, nil, value.List(value.Int(2), value.Int(3)))
	wantEq(t, `[1, 2, 3][5]`, nil, value.Null())
	wantEq(t, `{name: "Ada"}.name`, nil, value.String("Ada"))
	wantEq(t, `{name: "Ada"}["name"]`, nil, value.String("Ada"))
	wantEq(t, `null.name`, nil, value.Null())
	wantEq(t, `[x IN [1, 2, 3] WHERE x > 1 | x * 10]`, nil, value.List(value.Int(20), value.Int(30)))
	wantEq(t, `ALL (x IN [1, 2] WHERE x > 0)`, nil, value.Bool(true))
	wantEq(t, `ALL (x IN [] WHERE x > 0)`, nil, value.Bool(true))
	wantEq(t, `ANY (x IN [] WHERE true)`, nil, value.Bool(false))
	wantEq(t, `NONE (x IN [1] WHERE x > 2)`, nil, value.Bool(true))
	wantEq(t, `SINGLE (x IN [1, 2, 3] WHERE x = 2)`, nil, value.Bool(true))
	wantEq(t, `ALL (x IN [null] WHERE x > 0)`, nil, value.Bool(false))
	wantEq(t, `REDUCE (s = 0, x IN [1, 2, 3] | s + x)`, nil, value.Int(6))
	wantEq(t, `CASE WHEN false THEN 1 WHEN true THEN 2 END`, nil, value.Int(2))
	wantEq(t, `CASE 1 WHEN null THEN 0 WHEN 1 THEN 2 ELSE 3 END`, nil, value.Int(2))
	wantEq(t, `CASE null WHEN null THEN 1 ELSE 2 END`, nil, value.Int(2))
	wantErr(t, `1[0]`, nil, gerr.InvalidArgument)
	wantErr(t, `[1, 2]["a"]`, nil, gerr.InvalidArgument)

	env := &Env{}
	env.Set("x", value.Int(9))
	wantEq(t, `[x IN [1] | x]`, env, value.List(value.Int(1)))
	if got, _ := env.lookup("x"); !value.Equal(got, value.Int(9)) {
		t.Fatal(got)
	}
	_, err := Eval(mustExpr(t, `$name`), env)
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatal(err)
	}
	env.Params = map[string]value.Value{"name": value.String("Ada")}
	wantEq(t, `$name`, env, value.String("Ada"))
}

func TestGraphFns(t *testing.T) {
	m := NewMem()
	m.AddNode(1, []string{"Person"}, map[string]value.Value{"name": value.String("Ada")})
	m.AddNode(2, []string{"Person"}, map[string]value.Value{"name": value.String("Bea")})
	m.AddNode(3, nil, nil)
	m.AddNode(4, nil, nil)
	m.AddEdge(10, 1, 2, "KNOWS", map[string]value.Value{"since": value.Int(2020)})
	m.AddEdge(11, 1, 3, "KNOWS", nil)
	m.AddEdge(12, 2, 4, "KNOWS", nil)
	m.AddEdge(13, 3, 4, "KNOWS", nil)
	m.AddEdge(14, 1, 1, "SELF", nil)
	env := &Env{Graph: m}
	env.Set("a", value.NodeRef(1))
	env.Set("b", value.NodeRef(4))
	env.Set("e", value.EdgeRef(10))
	wantEq(t, `id(a)`, env, value.Int(1))
	wantEq(t, `labels(a)`, env, value.List(value.String("Person")))
	wantEq(t, `a.name`, env, value.String("Ada"))
	wantEq(t, `a.missing`, env, value.Null())
	wantEq(t, `keys(a)`, env, value.List(value.String("name")))
	wantEq(t, `type(e)`, env, value.String("KNOWS"))
	wantEq(t, `id(startNode(e))`, env, value.Int(1))
	wantEq(t, `id(endNode(e))`, env, value.Int(2))
	wantEq(t, `e.since`, env, value.Int(2020))
	wantEq(t, `properties(e).since`, env, value.Int(2020))
	wantEq(t, `degree(a)`, env, value.Int(3))
	wantEq(t, `degree(a, "in")`, env, value.Int(1))
	wantEq(t, `degree(a, "both")`, env, value.Int(3))
	wantEq(t, `neighbors(a, 1)`, env, value.List(value.NodeRef(2), value.NodeRef(3)))
	path := evalSrc(t, `shortestPath(a, b)`, env)
	nodes, edges, ok := path.PathValue()
	if !ok || len(edges) != 2 || edges[0] != 10 || edges[1] != 12 {
		t.Fatalf("shortest %s", path)
	}
	if nodes[0] != 1 || nodes[2] != 4 {
		t.Fatal(nodes)
	}
	all := evalSrc(t, `allShortestPaths(a, b)`, env)
	list, _ := all.ListValue()
	if len(list) != 2 {
		t.Fatalf("all shortest %s", all)
	}
	_, e0, _ := list[0].PathValue()
	_, e1, _ := list[1].PathValue()
	if e0[0] != 10 || e1[0] != 11 {
		t.Fatalf("order %s", all)
	}
	wantEq(t, `length(shortestPath(a, b))`, env, value.Int(2))
	wantEq(t, `shortestPath(b, a)`, env, value.Null())
	wantEq(t, `allShortestPaths(b, a)`, env, value.List())
	wantEq(t, `length(shortestPath(a, a))`, env, value.Int(0))
	wantErr(t, `labels(e)`, env, gerr.InvalidArgument)
	wantErr(t, `degree(a, "side")`, env, gerr.InvalidArgument)
}

func TestFromGraph(t *testing.T) {
	g := graph.New()
	n, err := g.CreateNode([]string{"Person"}, map[string]value.Value{"name": value.String("Ada")})
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{Graph: FromGraph(g)}
	env.Set("n", value.NodeRef(n.ID))
	wantEq(t, `n.name`, env, value.String("Ada"))
}

func TestAgg(t *testing.T) {
	c, err := NewAgg("count", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := c.AddRow(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.Value()
	if err != nil || !value.Equal(got, value.Int(3)) {
		t.Fatal(got, err)
	}

	cd, err := NewAgg("count", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []value.Value{value.Int(1), value.Null(), value.Int(1), value.Int(2)} {
		if err := cd.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	got, err = cd.Value()
	if err != nil || !value.Equal(got, value.Int(2)) {
		t.Fatal(got, err)
	}

	s, _ := NewAgg("sum", false)
	_ = s.Add(value.Int(1))
	_ = s.Add(value.Null())
	_ = s.Add(value.Int(2))
	got, _ = s.Value()
	if !value.Equal(got, value.Int(3)) {
		t.Fatal(got)
	}
	sf, _ := NewAgg("sum", false)
	_ = sf.Add(value.Int(1))
	_ = sf.Add(value.Float(2.5))
	got, _ = sf.Value()
	if !value.Equal(got, value.Float(3.5)) {
		t.Fatal(got)
	}
	empty, _ := NewAgg("sum", false)
	got, _ = empty.Value()
	if !value.Equal(got, value.Null()) {
		t.Fatal(got)
	}
	ov, _ := NewAgg("sum", false)
	_ = ov.Add(value.Int(math.MaxInt64))
	if err := ov.Add(value.Int(1)); err != nil {
		t.Fatal(err)
	}
	_, err = ov.Value()
	if gerr.CodeOf(err) != gerr.InvalidArgument {
		t.Fatal(err)
	}

	avg, _ := NewAgg("avg", false)
	_ = avg.Add(value.Int(1))
	_ = avg.Add(value.Int(2))
	got, _ = avg.Value()
	if !value.Equal(got, value.Float(1.5)) {
		t.Fatal(got)
	}

	mn, _ := NewAgg("min", false)
	_ = mn.Add(value.Int(3))
	_ = mn.Add(value.Null())
	_ = mn.Add(value.Int(1))
	got, _ = mn.Value()
	if !value.Equal(got, value.Int(1)) {
		t.Fatal(got)
	}

	col, _ := NewAgg("collect", true)
	_ = col.Add(value.Int(1))
	_ = col.Add(value.Null())
	_ = col.Add(value.Int(1))
	_ = col.Add(value.Int(2))
	got, _ = col.Value()
	if !value.Equal(got, value.List(value.Int(1), value.Int(2))) {
		t.Fatal(got)
	}

	p, err := NewAgg("percentileDisc", false, value.Float(0.5))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{1, 4, 2, 3} {
		_ = p.Add(value.Int(n))
	}
	got, err = p.Value()
	if err != nil || !value.Equal(got, value.Int(2)) {
		t.Fatal(got, err)
	}

	sd, _ := NewAgg("stdev", false)
	_ = sd.Add(value.Int(1))
	_ = sd.Add(value.Int(2))
	_ = sd.Add(value.Int(3))
	got, err = sd.Value()
	if err != nil || !value.Equal(got, value.Float(1)) {
		t.Fatal(got, err)
	}
	one, _ := NewAgg("stdev", false)
	_ = one.Add(value.Int(1))
	got, _ = one.Value()
	if !value.Equal(got, value.Null()) {
		t.Fatal(got)
	}
}

func mustDate(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustDur(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.ParseDuration(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

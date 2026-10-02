package repl

import (
	"bytes"
	"strings"
	"testing"
)

func TestREPLSession(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		`CREATE NODE person {name: "Alice", age: 30}`,
		`CREATE NODE person {name: "Bob", age: 20}`,
		`CREATE EDGE 1 -KNOWS-> 2 {since: 2020}`,
		`MATCH person WHERE age > 25`,
		`EDGES 1 TO 2`,
		`MATCH EDGE KNOWS`,
		`SHOW STATS`,
		`EXIT`,
	}, "\n") + "\n")
	var out bytes.Buffer
	err := Run(Config{
		In:  in,
		Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "created node 1") {
		t.Fatalf("missing create: %s", s)
	}
	if !strings.Contains(s, "Alice") {
		t.Fatalf("missing match output: %s", s)
	}
	if !strings.Contains(s, "KNOWS") {
		t.Fatalf("missing edge output: %s", s)
	}
	if !strings.Contains(s, "nodes:  2") {
		t.Fatalf("missing stats: %s", s)
	}
	if !strings.Contains(s, "bye") {
		t.Fatalf("missing exit: %s", s)
	}
}

func TestREPLSaveIsRejected(t *testing.T) {
	in := strings.NewReader("CREATE NODE person {name: \"Alice\"}\nSAVE g.db\nEXIT\n")
	var out bytes.Buffer
	if err := Run(Config{In: in, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "use `graphdb backup`") {
		t.Fatalf("expected backup hint, got: %s", out.String())
	}
}

func TestPipesSkipLineEditor(t *testing.T) {
	if isTerminal(strings.NewReader("EXIT\n")) {
		t.Fatal("piped input must keep using the scanner path")
	}
}

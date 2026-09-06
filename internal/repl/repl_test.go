package repl

import (
	"bytes"
	"path/filepath"
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

func TestREPLRecoverWAL(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "g.db")
	wal := filepath.Join(dir, "g.wal")

	in1 := strings.NewReader(`CREATE NODE person {name: "Alice"}` + "\nEXIT\n")
	var out1 bytes.Buffer
	if err := Run(Config{DBPath: db, WALPath: wal, In: in1, Out: &out1}); err != nil {
		t.Fatal(err)
	}

	in2 := strings.NewReader("MATCH person\nEXIT\n")
	var out2 bytes.Buffer
	if err := Run(Config{DBPath: db, WALPath: wal, In: in2, Out: &out2}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "Alice") {
		t.Fatalf("expected wal replay, got: %s", out2.String())
	}
	if !strings.Contains(out2.String(), "replayed") {
		t.Fatalf("expected replay notice: %s", out2.String())
	}
}

func TestPipesSkipLineEditor(t *testing.T) {
	if isTerminal(strings.NewReader("EXIT\n")) {
		t.Fatal("piped input must keep using the scanner path")
	}
}

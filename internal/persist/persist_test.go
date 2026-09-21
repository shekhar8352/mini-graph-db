package persist

import (
	"path/filepath"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/graph"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	g := graph.New()
	a := g.AddNode("person", map[string]any{"name": "Alice", "age": int64(30), "ok": true, "score": 1.5})
	b := g.AddNode("person", map[string]any{"name": "Bob"})
	if _, err := g.AddEdge(a.ID, b.ID, "KNOWS", map[string]any{"since": int64(2020)}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "graph.db")
	if err := Save(g, path); err != nil {
		t.Fatal(err)
	}

	g2 := graph.New()
	if err := Load(g2, path); err != nil {
		t.Fatal(err)
	}
	st := g2.Stats()
	if st.Nodes != 2 || st.Edges != 1 {
		t.Fatalf("stats %+v", st)
	}
	n, ok := g2.GetNode(1)
	if !ok || n.Props["name"] != "Alice" || n.Props["age"] != int64(30) || n.Props["ok"] != true {
		t.Fatalf("node props %+v", n.Props)
	}
	next := g2.AddNode("person", nil)
	if next.ID != 3 {
		t.Fatalf("id counter: %d", next.ID)
	}
}

func TestWALAppendReadTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.wal")
	w, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(`CREATE NODE person {name: "A"}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(`CREATE NODE person {name: "B"}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %v", lines)
	}

	w, err = OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Truncate(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	lines, err = ReadWAL(path)
	if err != nil || len(lines) != 0 {
		t.Fatalf("truncated wal: %v %v", lines, err)
	}
}

func TestOpenWALCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "graph.wal")
	w, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(`CREATE NODE person {name: "A"}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadWAL(path)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}

func TestReadMissingWAL(t *testing.T) {
	lines, err := ReadWAL(filepath.Join(t.TempDir(), "nope.wal"))
	if err != nil || lines != nil {
		t.Fatalf("missing wal should be empty: %v %v", lines, err)
	}
}

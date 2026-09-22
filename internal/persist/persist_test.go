package persist

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/value"
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
	if !ok || !persistProp(n, "name", value.String("Alice")) || !persistProp(n, "age", value.Int(30)) || !persistProp(n, "ok", value.Bool(true)) {
		t.Fatalf("node props %+v", n.Properties())
	}
	if n.Label() != "person" {
		t.Fatalf("label %q", n.Label())
	}
	next := g2.AddNode("person", nil)
	if next.ID != 3 {
		t.Fatalf("id counter: %d", next.ID)
	}
}

func TestLoadLegacySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := encodedSnapshot{
		Version:  0,
		NextNode: 2,
		Nodes: []encodedNode{{
			ID:    1,
			Label: "person",
			Props: []encodedProp{{Key: "name", Kind: "s", Str: "Ada"}, {Key: "age", Kind: "i", Int: 36}},
		}},
	}
	if err := gob.NewEncoder(f).Encode(enc); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	g := graph.New()
	if err := Load(g, path); err != nil {
		t.Fatal(err)
	}
	n, ok := g.GetNode(1)
	if !ok || n.Label() != "person" || !persistProp(n, "name", value.String("Ada")) || !persistProp(n, "age", value.Int(36)) {
		t.Fatalf("legacy node %+v", n.Properties())
	}
}

func TestRejectNewerSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := encodedSnapshot{Version: snapshotVersion + 1}
	if err := gob.NewEncoder(f).Encode(enc); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Load(graph.New(), path); err == nil {
		t.Fatal("expected newer snapshot to be rejected")
	}
}

func persistProp(n graph.Node, key string, want value.Value) bool {
	got, ok := n.Prop(key)
	return ok && value.Equal(got, want)
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

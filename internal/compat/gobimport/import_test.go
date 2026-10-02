package gobimport

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func TestImportSnapshotAndWAL(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "graph.db")
	walPath := filepath.Join(dir, "graph.wal")
	dest := filepath.Join(dir, "out")

	g := graph.New()
	if _, err := g.CreateNode([]string{"person", "user"}, map[string]value.Value{
		"name": value.String("Alice"),
		"age":  value.Int(30),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateNode([]string{"person"}, map[string]value.Value{"name": value.String("Bob")}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateEdge(1, 2, "KNOWS", map[string]value.Value{"since": value.Int(2020)}); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshot(snapPath, g.Export()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath, []byte("# note\n\nCREATE NODE person {name: \"Cara\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Import(snapPath, walPath, dest); err != nil {
		t.Fatal(err)
	}

	eng, err := disk.OpenEngine(dest, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	got := graph.NewWith(eng)
	if got.Stats().Nodes != 3 || got.Stats().Edges != 1 {
		t.Fatalf("stats %+v", got.Stats())
	}
	n, ok := got.GetNode(1)
	if !ok || !n.HasLabel("user") || !n.HasLabel("person") {
		t.Fatalf("labels %v", n.Labels())
	}
	v, ok := n.Prop("age")
	if !ok || !value.Equal(v, value.Int(30)) {
		t.Fatalf("age %v", v)
	}
	next := got.AddNode("person", nil)
	if next.ID != 4 {
		t.Fatalf("next id %d", next.ID)
	}
}

func TestImportLegacyVersion0(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "legacy.db")
	f, err := os.Create(snapPath)
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
	dest := filepath.Join(dir, "out")
	if err := Import(snapPath, "", dest); err != nil {
		t.Fatal(err)
	}
	eng, err := disk.OpenEngine(dest, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	n, ok := graph.NewWith(eng).GetNode(1)
	if !ok || n.Label() != "person" {
		t.Fatalf("node %+v", n.Labels())
	}
	v, ok := n.Prop("name")
	if !ok || !value.Equal(v, value.String("Ada")) {
		t.Fatalf("name %v", v)
	}
}

func TestImportRejectsNewerSnapshot(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "new.db")
	f, err := os.Create(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(encodedSnapshot{Version: snapshotVersion + 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	err = Import(snapPath, "", dest)
	if !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest created: %v", statErr)
	}
}

func TestImportRefusesExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	eng, err := disk.OpenEngine(dest, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	snapPath := filepath.Join(dir, "graph.db")
	if err := writeSnapshot(snapPath, graph.New().Export()); err != nil {
		t.Fatal(err)
	}
	if err := Import(snapPath, "", dest); !gerr.IsCode(err, gerr.AlreadyExists) {
		t.Fatalf("got %v", err)
	}
}

func TestImportReplayErrorLeavesNoDatabase(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "graph.db")
	walPath := filepath.Join(dir, "graph.wal")
	if err := writeSnapshot(snapPath, graph.New().Export()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath, []byte("FLORB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := Import(snapPath, walPath, dest); err == nil {
		t.Fatal("expected replay error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("dest created: %v", err)
	}
}

func writeSnapshot(path string, snap graph.Snapshot) error {
	enc := encodedSnapshot{
		Version:  snapshotVersion,
		NextNode: snap.NextNode,
		NextEdge: snap.NextEdge,
		PropKeys: snap.PropKeys,
	}
	for _, n := range snap.Nodes {
		labels := n.Labels()
		node := encodedNode{ID: n.ID, Labels: labels, Props: encodeProps(n.PropList())}
		if len(labels) > 0 {
			node.Label = labels[0]
		}
		enc.Nodes = append(enc.Nodes, node)
	}
	for _, e := range snap.Edges {
		enc.Edges = append(enc.Edges, encodedEdge{
			ID: e.ID, From: e.From, To: e.To, Label: e.Label, Props: encodeProps(e.PropList()),
		})
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return gob.NewEncoder(f).Encode(enc)
}

func encodeProps(props []graph.Prop) []encodedProp {
	out := make([]encodedProp, 0, len(props))
	for _, p := range props {
		out = append(out, encodedProp{
			Key:    p.Name,
			KeyID:  p.ID,
			Kind:   "r",
			Record: value.EncodeRecord(p.Value),
		})
	}
	return out
}

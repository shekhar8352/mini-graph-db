package main

import (
	"bytes"
	"encoding/gob"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/graph"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
	"github.com/shekhar8352/mini-graph-db/internal/value"
	"github.com/shekhar8352/mini-graph-db/internal/version"
)

func TestVersionCommand(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, version.Version) {
		t.Fatalf("missing semver in %q", got)
	}
	if !strings.Contains(got, version.Commit) {
		t.Fatalf("missing commit in %q", got)
	}
}

func TestShellUsesDataDir(t *testing.T) {
	dir := t.TempDir()
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader("SHOW STATS\nEXIT\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"shell", "--data-dir", dir, "--log-level", "error"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nodes:") {
		t.Fatalf("expected stats output, got %q", out.String())
	}
}

func TestRootDefaultsToShell(t *testing.T) {
	dir := t.TempDir()
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader("EXIT\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--data-dir", dir, "--log-level", "error"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bye") {
		t.Fatalf("expected default shell session, got %q", out.String())
	}
}

func TestResolvePathsOverrides(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "nested", "custom.history")
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader("EXIT\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--data-dir", dir,
		"--history", hist,
		"--log-level", "error",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(hist)); err != nil {
		t.Fatalf("history parent: %v", err)
	}
}

func TestMigrateCommand(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "legacy.db")
	wal := filepath.Join(dir, "legacy.wal")
	dest := filepath.Join(dir, "dbdir")
	f, err := os.Create(snap)
	if err != nil {
		t.Fatal(err)
	}
	enc := legacySnapshot{
		Version:  1,
		NextNode: 2,
		Nodes: []legacyNode{{
			ID:     1,
			Label:  "person",
			Labels: []string{"person"},
			Props:  []legacyProp{{Key: "name", Kind: "s", Str: "Ada"}},
		}},
	}
	if err := gob.NewEncoder(f).Encode(enc); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wal, []byte("CREATE NODE person {name: \"Bea\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"migrate", "--from-legacy", snap, "--wal", wal, "--to", dest, "--log-level", "error"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	eng, err := disk.OpenEngine(dest, disk.EngineOptions{CheckpointInterval: -1, CheckpointBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	g := graph.NewWith(eng)
	n, ok := g.GetNode(1)
	if !ok {
		t.Fatal("missing migrated node")
	}
	v, ok := n.Prop("name")
	if !ok || !value.Equal(v, value.String("Ada")) {
		t.Fatalf("name %v", v)
	}
	n, ok = g.GetNode(2)
	if !ok {
		t.Fatal("missing replayed node")
	}
	v, ok = n.Prop("name")
	if !ok || !value.Equal(v, value.String("Bea")) {
		t.Fatalf("replayed name %v", v)
	}
}

type legacyProp struct {
	Key, Kind, Str string
	KeyID          uint32
	Int            int64
	Float          float64
	Bool           bool
	Record         []byte
}

type legacyNode struct {
	ID     uint64
	Label  string
	Labels []string
	Props  []legacyProp
}

type legacySnapshot struct {
	Version  int
	Nodes    []legacyNode
	Edges    []legacyEdge
	NextNode uint64
	NextEdge uint64
	PropKeys []string
}

type legacyEdge struct {
	ID    uint64
	From  uint64
	To    uint64
	Label string
	Props []legacyProp
}

func TestInvalidLogLevel(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"version", "--log-level", "nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid log-level to fail")
	}
}

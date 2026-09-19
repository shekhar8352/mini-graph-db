package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if _, err := os.Stat(filepath.Join(dir, "graph.wal")); err != nil {
		t.Fatalf("expected WAL under data-dir: %v", err)
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
	db := filepath.Join(dir, "custom.db")
	wal := filepath.Join(dir, "custom.wal")
	hist := filepath.Join(dir, "custom.history")
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader("EXIT\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--data-dir", dir,
		"--db", db,
		"--wal", wal,
		"--history", hist,
		"--log-level", "error",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wal); err != nil {
		t.Fatalf("custom wal: %v", err)
	}
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

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Bind != DefaultBind {
		t.Fatalf("bind %s", cfg.Server.Bind)
	}
	if cfg.Server.MaxConnections != 1000 {
		t.Fatalf("connections %d", cfg.Server.MaxConnections)
	}
	if !cfg.Storage.Fsync {
		t.Fatal("fsync default")
	}
	text := DefaultTOML()
	if text == "" || len(text) < 100 {
		t.Fatalf("defaults toml %q", text)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graphdb.toml")
	body := `
[server]
bind = "127.0.0.1:1"
max_connections = 4

[storage]
data_dir = "from-file"
fsync = false

[log]
level = "debug"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAPHDB_STORAGE_DATA_DIR", "from-env")
	t.Setenv("GRAPHDB_SERVER_MAX_CONNECTIONS", "8")
	cfg, err := Load(path, Flags{BindSet: true, Bind: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Bind != "127.0.0.1:9" {
		t.Fatalf("flag bind %s", cfg.Server.Bind)
	}
	if cfg.Storage.DataDir != "from-env" {
		t.Fatalf("env data %s", cfg.Storage.DataDir)
	}
	if cfg.Server.MaxConnections != 8 {
		t.Fatalf("env connections %d", cfg.Server.MaxConnections)
	}
	if cfg.Storage.Fsync {
		t.Fatal("file fsync should stay false")
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("level %s", cfg.Log.Level)
	}
	if cfg.Limits.QueryTimeout != 30*time.Second {
		t.Fatalf("timeout %s", cfg.Limits.QueryTimeout)
	}
}

func TestRejectsPublicBindWithoutTLS(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Bind = "0.0.0.0:9530"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
	cfg.Server.InsecurePlaintext = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graphdb.toml")
	if err := os.WriteFile(path, []byte("[server]\nnope = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, Flags{}); err == nil {
		t.Fatal("expected unknown key")
	}
}

func TestFsyncFalseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graphdb.toml")
	if err := os.WriteFile(path, []byte("[storage]\nfsync = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Fsync {
		t.Fatal("fsync")
	}
	lines := cfg.RedactedLines()
	found := false
	for _, line := range lines {
		if line == "fsync=false" {
			found = true
		}
	}
	if !found {
		t.Fatalf("banner %v", lines)
	}
}

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "", want: slog.LevelInfo},
		{in: "info", want: slog.LevelInfo},
		{in: "DEBUG", want: slog.LevelDebug},
		{in: "warn", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "trace", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseLevel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(Config{Level: "info", Format: "json", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hello", "k", 1)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("json: %v raw=%q", err, buf.String())
	}
	if rec["msg"] != "hello" {
		t.Fatalf("msg=%v", rec["msg"])
	}
}

func TestTextHandlerFiltersDebug(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(Config{Level: "warn", Format: "text", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("hidden")
	log.Warn("visible")
	s := buf.String()
	if strings.Contains(s, "hidden") {
		t.Fatalf("debug leaked: %q", s)
	}
	if !strings.Contains(s, "visible") {
		t.Fatalf("missing warn: %q", s)
	}
}

func TestInvalidFormat(t *testing.T) {
	if _, err := New(Config{Format: "xml"}); err == nil {
		t.Fatal("expected error")
	}
}

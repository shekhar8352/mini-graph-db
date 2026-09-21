package version

import (
	"strings"
	"testing"
)

func TestStringContainsSemverAndCommit(t *testing.T) {
	s := String()
	if !strings.Contains(s, Version) {
		t.Fatalf("missing version in %q", s)
	}
	if !strings.Contains(s, Commit) {
		t.Fatalf("missing commit in %q", s)
	}
}

func TestDefaults(t *testing.T) {
	if Version == "" || Commit == "" || BuildDate == "" {
		t.Fatal("version fields must have defaults for go run builds")
	}
}

// Package version reports the binary's semver, git commit, and build date.
// Values are typically injected at link time via -ldflags.
package version

import "fmt"

// These are overridden with -ldflags at build time:
//
//	-X github.com/shekhar8352/mini-graph-db/internal/version.Version=...
//	-X github.com/shekhar8352/mini-graph-db/internal/version.Commit=...
//	-X github.com/shekhar8352/mini-graph-db/internal/version.BuildDate=...
var (
	Version   = "0.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns a one-line version summary: semver + commit + build date.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, BuildDate)
}

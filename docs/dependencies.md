# Dependencies

Core engine packages stay stdlib-only: `internal/value` (Phase 1) and `internal/storage` including `memory`, `enginetest`, and `graphstore` (Phase 2A). `internal/wal`, `internal/txn`, `internal/lang`, `internal/exec`, `internal/index`, and `internal/catalog` stay stdlib-only when they arrive. `internal/graph`, `internal/query`, and `internal/persist` are also stdlib-only today.

Anything else requires an allowlist entry here and a one-line justification in the commit message. New libraries not on the roadmap allowlist need an ADR.

## Direct dependencies

| Module | Used by | Justification |
|--------|---------|---------------|
| `github.com/peterh/liner` | `internal/repl` | Terminal line editing for the embedded shell. Already present; on the Phase 0 allowlist. |
| `github.com/spf13/cobra` | `cmd/graphdb` | CLI subcommands (`shell`, `version`). Required by Phase 0 task 0.10; on the allowlist. |

## Dev / CI tools (not imported by the module)

| Tool | Justification |
|------|---------------|
| `golangci-lint` v2 | `make lint` and GitHub Actions. Allowlisted. |
| `golang.org/x/tools/cmd/goimports` v0.37.0 | `make fmt` (pulled via `go run` when not installed). Pinned so it stays on Go 1.25. |

## Indirect

Cobra and liner pull `github.com/spf13/pflag`, `github.com/mattn/go-runewidth`, and `golang.org/x/sys`. Do not import those directly unless a task needs them.

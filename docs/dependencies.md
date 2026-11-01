# Dependencies

Core engine packages stay stdlib-only: `internal/value` (Phase 1), `internal/storage` including `memory`, `enginetest`, `graphstore`, and `disk` (Phase 2), and `internal/wal` (Phase 2D). `internal/txn`, `internal/lang`, `internal/exec`, `internal/index`, and `internal/catalog` stay stdlib-only. `internal/graph`, `internal/query`, and `internal/compat/gobimport` are also stdlib-only. `internal/config` and `internal/server` sit outside that set and may import the allowlisted modules below.

Anything else requires an allowlist entry here and a one-line justification in the commit message. New libraries not on the roadmap allowlist need an ADR.

## Direct dependencies

| Module | Used by | Justification |
|--------|---------|---------------|
| `github.com/peterh/liner` | `internal/repl` | Terminal line editing for the embedded shell. Already present; on the Phase 0 allowlist. |
| `github.com/spf13/cobra` | `cmd/graphdb` | CLI subcommands (`shell`, `server`, `config`, `admin`, `version`). Required by Phase 0 task 0.10; on the allowlist. |
| `github.com/BurntSushi/toml` | `internal/config` | TOML config file. On the Phase 0 allowlist for configuration. |
| `google.golang.org/grpc` | `internal/server` | gRPC server, health, and reflection. On the Phase 0 allowlist. |
| `google.golang.org/protobuf` | `api/gen`, `internal/server` | Generated messages and `proto.Size` for the row-batch cap. On the Phase 0 allowlist. |

## Dev / CI tools (not imported by the module)

| Tool | Justification |
|------|---------------|
| `golangci-lint` v2 | `make lint` and GitHub Actions. Allowlisted. |
| `golang.org/x/tools/cmd/goimports` v0.37.0 | `make fmt` (pulled via `go run` when not installed). Pinned so it stays on Go 1.25. |
| `buf` v1.57.2 | `make proto`. Compiles `api/proto`. On the Phase 0 allowlist. |
| `protoc-gen-go` v1.36.6, `protoc-gen-go-grpc` v1.5.1 | `make proto`. Local plugins so generation does not call a remote plugin. |

## Indirect

Cobra and liner pull `github.com/spf13/pflag`, `github.com/mattn/go-runewidth`, and `golang.org/x/sys`. gRPC pulls `golang.org/x/net`, `golang.org/x/text`, and `google.golang.org/genproto`. Do not import those directly unless a task needs them.

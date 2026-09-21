# Changelog

All notable work on this repository is recorded here. Task IDs match [ROADMAP.md](../ROADMAP.md).

## Unreleased

### Phase 0 — Foundations, repo hygiene, CI (2026-09-19)

- **0.1** Module path is `github.com/shekhar8352/mini-graph-db` ([ADR 0001](adr/0001-module-path.md)). All imports updated.
- **0.2** `Makefile` targets: `build`, `test` (`-race -count=1`), `lint`, `fmt`, `proto` (placeholder), `bench`, `cover`.
- **0.3** GitHub Actions CI on Go 1.25, Linux and macOS, running `make lint test` with module caching.
- **0.4** `.golangci.yml` enables govet, staticcheck, errcheck, gosimple, ineffassign, unused, misspell, revive, gofmt, and goimports.
- **0.5** `internal/version` injects semver, git commit, and build date via `-ldflags`; `graphdb version` prints them.
- **0.6** `internal/gerr` typed error codes with `Retryable()`. NotFound / Syntax / InvalidArgument wired into graph, query, and traversals.
- **0.7** `internal/logging` wraps `log/slog` (text and JSON). `--log-level` and `--log-format` flags on the CLI; recovery events logged from the shell.
- **0.8** `docs/` skeleton: index, ADR template, ADR 0001, changelog, backlog, dependencies, baseline architecture.
- **0.9** Default snapshot, WAL, and history files live under `./data` (`--data-dir`). Parent directories are created as needed. `.gitignore` covers `/data/`, `/bin/`, and coverage files.
- **0.10** `cmd/graphdb` is a cobra CLI: `graphdb` and `graphdb shell` start the REPL; `graphdb version` prints build info.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P10 Change management | Module path is the public import identity; on-disk formats unchanged. |
| P12 Testing | CI runs `go test -race` and golangci-lint on Linux and macOS. |
| P13 Documentation | Docs skeleton and README CLI section updated. |
| P1–P9, P11 | Not yet applicable (no networked server, no new durability guarantees). |

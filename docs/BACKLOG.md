# Backlog

Items that are out of scope for 1.0, or discovered during a phase but not part of the current task. Do not implement these unless a later phase or an ADR says so.

## Explicitly out of scope for 1.0

Copied from the roadmap so they are not "fixed" opportunistically:

- openCypher / Gremlin / Bolt compatibility.
- REST or WebSocket APIs.
- Non-Go client SDKs.
- Multi-primary or consensus-based clustering; automatic failover.
- Property-level and label-level access control; row-level security.
- Full-text and vector indexes.
- Stored procedures / user-defined functions beyond the built-in `CALL db.*` procedures.
- Encryption at rest (rely on filesystem/volume encryption; document in Phase 8).
- Graph algorithms library (PageRank, community detection) — traversal and shortest paths only.

## Discovered during Phase 0

- `internal/persist.OpenWAL` now creates parent directories; consider the same for REPL history writes if `--history` is pointed at a missing nested path without going through `graphdb shell` (shell already mkdirs).
- golangci-lint v2 treats `gofmt` / `goimports` as formatters rather than linters, and folds `gosimple` into `staticcheck`. `.golangci.yml` follows that mapping while covering the Phase 0 requested set.

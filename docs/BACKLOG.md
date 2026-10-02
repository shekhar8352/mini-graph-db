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

## Discovered during Phase 2E

- A crash while the heap header is being published can leave the file shorter than the header's page count. Open fails before WAL replay, even when the log has the commit. Repairing page 0 from the latest committed header image is not implemented.
- A crash during allocate can leak a page. The freelist is not rebuilt, matching [ADR 0004](adr/0004-page-file.md).
- `Get`, `Put`, `Delete`, and `Commit` on a keyspace deadlock if the same goroutine still has a cursor open on that tree.
- Committed redo is a full page image, so a one-key update logs every page it dirties.

## Discovered during Phase 0

- golangci-lint v2 treats `gofmt` / `goimports` as formatters rather than linters, and folds `gosimple` into `staticcheck`. `.golangci.yml` follows that mapping while covering the Phase 0 requested set.

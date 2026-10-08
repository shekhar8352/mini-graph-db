# Documentation

This directory is the documentation root for `graphdb`. The [ROADMAP](../ROADMAP.md) is the execution plan; the files here are the living product docs.

## Index

| Document | Purpose |
|----------|---------|
| [architecture.md](architecture.md) | Baseline architecture of the embedded engine (pre–Phase 1) |
| [spec/values.md](spec/values.md) | Typed value model (Phase 1) |
| [spec/pages.md](spec/pages.md) | Page file and B+tree layout (Phase 2B–2C) |
| [spec/wal.md](spec/wal.md) | Binary write-ahead log (Phase 2D) |
| [spec/engine.md](spec/engine.md) | Disk engine commit and recovery (Phase 2E) |
| [spec/legacy.md](spec/legacy.md) | Legacy gob snapshot import (Phase 2F) |
| [spec/transactions.md](spec/transactions.md) | Snapshot isolation and versions (Phase 3) |
| [spec/query-language.md](spec/query-language.md) | GQL-lite grammar version 1 (Phase 4.1) |
| [CHANGELOG.md](CHANGELOG.md) | Per-task history |
| [BACKLOG.md](BACKLOG.md) | Out-of-scope ideas and follow-ups discovered during work |
| [dependencies.md](dependencies.md) | Allowed third-party modules and justifications |
| [adr/](adr/) | Architecture Decision Records |

Operations guides (`docs/ops/`) and SDK docs (`docs/sdk/`) are added in later phases. The value spec landed in Phase 1. The page-file spec landed in Phase 2B. The B+tree layout landed in Phase 2C. The binary WAL spec landed in Phase 2D. The disk engine spec landed in Phase 2E. The legacy import spec landed in Phase 2F. The transaction spec landed in Phase 3. The GQL-lite grammar landed in Phase 4.1. The lexer landed in Phase 4.2. The AST and printer landed in Phase 4.3. The Pratt parser, error recovery, and example corpus landed in Phase 4.4. The semantic checker landed in Phase 4.5. It checks scope, clause order, and expression kinds, and it does not run the query. The shell still runs the legacy line language.

## ADR index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](adr/0000-template.md) | Template | — |
| [0001](adr/0001-module-path.md) | Go module path | Accepted |
| [0002](adr/0002-value-model.md) | Value model, ordering, and encodings | Accepted |
| [0003](adr/0003-storage-engine.md) | Storage engine interface and memory snapshots | Accepted |
| [0004](adr/0004-page-file.md) | Page file, freelist, and buffer pool | Accepted |
| [0005](adr/0005-btree.md) | B+tree shape, delete, and overflow | Accepted |
| [0006](adr/0006-wal.md) | Binary write-ahead log | Accepted |
| [0007](adr/0007-disk-engine.md) | Disk engine, page-image redo, and recovery | Accepted |
| [0008](adr/0008-legacy-import.md) | Legacy gob import | Accepted |
| [0009](adr/0009-mvcc.md) | MVCC version placement and snapshot isolation | Accepted |
| [0010](adr/0010-gql-lite.md) | GQL-lite grammar | Accepted |

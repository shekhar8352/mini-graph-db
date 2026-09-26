# Documentation

This directory is the documentation root for `graphdb`. The [ROADMAP](../ROADMAP.md) is the execution plan; the files here are the living product docs.

## Index

| Document | Purpose |
|----------|---------|
| [architecture.md](architecture.md) | Baseline architecture of the embedded engine (pre–Phase 1) |
| [spec/values.md](spec/values.md) | Typed value model (Phase 1) |
| [spec/pages.md](spec/pages.md) | Page file and B+tree layout (Phase 2B–2C) |
| [CHANGELOG.md](CHANGELOG.md) | Per-task history |
| [BACKLOG.md](BACKLOG.md) | Out-of-scope ideas and follow-ups discovered during work |
| [dependencies.md](dependencies.md) | Allowed third-party modules and justifications |
| [adr/](adr/) | Architecture Decision Records |

Operations guides (`docs/ops/`) and SDK docs (`docs/sdk/`) are added in later phases. The value spec landed in Phase 1. The page-file spec landed in Phase 2B. The B+tree layout landed in Phase 2C.

## ADR index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](adr/0000-template.md) | Template | — |
| [0001](adr/0001-module-path.md) | Go module path | Accepted |
| [0002](adr/0002-value-model.md) | Value model, ordering, and encodings | Accepted |
| [0003](adr/0003-storage-engine.md) | Storage engine interface and memory snapshots | Accepted |
| [0004](adr/0004-page-file.md) | Page file, freelist, and buffer pool | Accepted |
| [0005](adr/0005-btree.md) | B+tree shape, delete, and overflow | Accepted |

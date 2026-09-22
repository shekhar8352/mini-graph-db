# Documentation

This directory is the documentation root for `graphdb`. The [ROADMAP](../ROADMAP.md) is the execution plan; the files here are the living product docs.

## Index

| Document | Purpose |
|----------|---------|
| [architecture.md](architecture.md) | Baseline architecture of the embedded engine (pre–Phase 1) |
| [spec/values.md](spec/values.md) | Typed value model (Phase 1) |
| [CHANGELOG.md](CHANGELOG.md) | Per-task history |
| [BACKLOG.md](BACKLOG.md) | Out-of-scope ideas and follow-ups discovered during work |
| [dependencies.md](dependencies.md) | Allowed third-party modules and justifications |
| [adr/](adr/) | Architecture Decision Records |

Operations guides (`docs/ops/`) and SDK docs (`docs/sdk/`) are added in later phases. The value spec landed in Phase 1.

## ADR index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](adr/0000-template.md) | Template | — |
| [0001](adr/0001-module-path.md) | Go module path | Accepted |
| [0002](adr/0002-value-model.md) | Value model, ordering, and encodings | Accepted |

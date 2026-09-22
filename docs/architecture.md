# Architecture (baseline)

This is a snapshot of the **pre–Phase 1** architecture of `mini-graph-db`: an embedded, in-memory property graph driven by a line-oriented REPL. Later phases replace persistence, the query language, and the process model; this file stays as the baseline so diffs against the original design remain readable.

Phase 1 added `internal/value` and multi-label nodes on top of this baseline. The current type rules are in [spec/values.md](spec/values.md); the README describes the engine as it runs today.

Copied from the README architecture section at the start of Phase 0.

## Diagram

```
                    ┌─────────────────────────────────────┐
                    │            cmd/graphdb              │
                    │     cobra: shell | version          │
                    │   flags: --data-dir --log-level     │
                    └─────────────────┬───────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │            internal/repl            │
                    │  recover snapshot + WAL, then loop  │
                    │  TTY → liner   |   pipe → scanner   │
                    └─────────────────┬───────────────────┘
                                      │ one line
                                      ▼
                    ┌─────────────────────────────────────┐
                    │           internal/query            │
                    │  lexer → parser → AST → executor    │
                    └──────────────┬──────────┬───────────┘
                                   │          │ mutating stmts
                                   ▼          ▼
                    ┌──────────────────┐  ┌──────────────────┐
                    │  internal/graph  │  │ internal/persist │
                    │  engine + indexes│  │  snapshot + WAL  │
                    └──────────────────┘  └──────────────────┘
```

Request path for a typical command:

1. The REPL reads a line (`graph> MATCH person WHERE age > 25`).
2. `query.Parse` lexes tokens and builds an AST (`MatchStmt`).
3. `query.Executor` calls the graph engine (`NodesByLabel` + `WHERE` compare).
4. If the statement mutated the graph, the raw line is appended to the WAL.
5. The REPL prints a text table (nodes, edges, neighbors, path, or stats).

Packages depend inward only: `cmd` → `repl` → `query` → (`graph`, `persist`) → `graph`. There are no third-party dependencies in the engine, parser, or persistence layer. The REPL uses `github.com/peterh/liner` solely for terminal line editing. The CLI uses `github.com/spf13/cobra`.

Cross-cutting packages added in Phase 0 (not on the request path):

- `internal/version` — build-time semver / commit / date
- `internal/gerr` — typed error codes
- `internal/logging` — `log/slog` setup (text or JSON)

### Package map

| Path | Role |
|------|------|
| `cmd/graphdb` | Process entry: cobra commands, flags, start the REPL |
| `internal/repl` | Prompt, recovery, history, table-formatted output |
| `internal/query` | Lexer, recursive-descent parser, AST, executor |
| `internal/graph` | Property graph, CRUD, indexes, BFS/DFS, shortest path |
| `internal/persist` | Gob snapshot encode/decode and WAL |
| `internal/gerr` | Typed error taxonomy |
| `internal/logging` | slog handlers |
| `internal/version` | Version string injected via ldflags |

## Data files (Phase 0)

Default `--data-dir` is `./data`. Unless overridden:

| File | Purpose |
|------|---------|
| `data/graph.db` | Gob snapshot (`SAVE` / `LOAD`) |
| `data/graph.wal` | Text WAL of mutating statements |
| `data/graph.history` | REPL line history |

`--db`, `--wal`, and `--history` still override individual paths.

## What this baseline does not have

See the roadmap: no page store, no transactions beyond a global mutex, no network server, no auth, no multi-label nodes, no typed value system beyond string/int64/float64/bool.

# mini-graph-db

An embedded, in-memory **property-graph database** written in Go. It stores labeled nodes and directed labeled edges, each with key-value properties, and is driven by an interactive REPL. The shell runs GQL-lite (grammar version 1) and still accepts the older line language by rewriting it.

The shell keeps the working set in RAM for one session. A durable database is a directory opened with `OpenEngine`. `graphdb migrate` loads an old gob snapshot into that directory.

```bash
make test
go run ./cmd/graphdb          # defaults to the interactive shell
go run ./cmd/graphdb version
```

Module path: `github.com/shekhar8352/mini-graph-db`. Docs live in [`docs/`](docs/README.md). The [roadmap](ROADMAP.md) is the plan for turning this into a networked, persistent graph database.

| Flag | Default | Purpose |
|------|---------|---------|
| `--data-dir` | `./data` | Directory for the REPL history file |
| `--history` | `<data-dir>/graph.history` | REPL command history (up-arrow) |
| `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-format` | `text` | `text` or `json` |

Commands: `graphdb` / `graphdb shell` start the REPL; `graphdb version` prints semver and git commit; `graphdb migrate --from-legacy <snapshot> [--wal <file>] --to <dir>` imports an old gob snapshot.
In a real terminal the prompt supports line editing: left/right move the cursor, up/down walk history, Tab completes keywords, Ctrl-C cancels the current line, Ctrl-D or `EXIT` leaves the REPL. Piped input uses a plain scanner (no line editor).

---

## Architecture

```
                    ┌─────────────────────────────────────┐
                    │            cmd/graphdb              │
                    │  cobra: shell | version | migrate   │
                    │   flags: --data-dir --log-level     │
                    └─────────────────┬───────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │            internal/repl            │
                    │  in-memory session, then loop       │
                    │  TTY → liner   |   pipe → scanner   │
                    └─────────────────┬───────────────────┘
                                      │ one line
                                      ▼
                    ┌─────────────────────────────────────┐
                    │  compat/legacy → lang → exec        │
                    │  GQL-lite, or a rewritten line      │
                    └─────────────────┬───────────────────┘
                                      ▼
                    ┌─────────────────────────────────────┐
                    │  internal/graph → graphstore        │
                    │  shell: txn on storage/memory       │
                    │  migrate: storage/disk.OpenEngine   │
                    └─────────────────────────────────────┘
```

Request path for a typical command:

1. The REPL reads a line (`graph> MATCH (n:person) WHERE n.age > 25 RETURN n` or the legacy `MATCH person WHERE age > 25`).
2. `legacy.Compile` accepts GQL-lite as written, or rewrites a legacy line to that AST.
3. `exec.Run` plans and pulls the statement. Without `BEGIN`, the statement is one transaction on the memory engine through the transaction manager.
4. The REPL prints a text table of columns and rows, or the familiar `nodes:` / `edges:` / `labels:` block for `SHOW STATS`.

Packages depend inward only: `cmd` → `repl` → (`compat/legacy`, `exec`) → `graph` → (`graphstore`, `txn`) → `storage`. `txn` does not import `graphstore`. `graphdb migrate` also calls `internal/compat/gobimport`, which writes `storage/disk`. There are no third-party dependencies in the engine, the transaction manager, or the parser. The REPL uses `github.com/peterh/liner` solely for terminal line editing.

### Package map

| Path | Role |
|------|------|
| [`cmd/graphdb`](cmd/graphdb) | Process entry: cobra CLI (`shell`, `migrate`, `version`) |
| [`internal/repl`](internal/repl/repl.go) | Prompt, history, table-formatted output |
| [`internal/compat/legacy`](internal/compat/legacy) | Legacy line-language rewrite into GQL-lite |
| [`internal/exec`](internal/exec) | GQL-lite executor used by the shell |
| [`internal/query`](internal/query) | Legacy line parser still used by the rewrite and by `graphdb migrate` |
| [`internal/value`](internal/value) | Typed values, ordering, key and record encodings |
| [`internal/storage`](internal/storage) | Key-value engine interface |
| [`internal/storage/memory`](internal/storage/memory) | In-memory engine (sorted keys per keyspace) |
| [`internal/storage/disk`](internal/storage/disk) | Page file, buffer pool, B+tree, and `OpenEngine` (not used by the shell yet) |
| [`internal/wal`](internal/wal) | Binary write-ahead log used by the disk engine |
| [`internal/storage/graphstore`](internal/storage/graphstore) | Nodes, edges, adjacency, indexes, catalog ids |
| [`internal/txn`](internal/txn) | Snapshot isolation, versions, and commit timestamps |
| [`internal/graph`](internal/graph) | Facade used by the query executor and the shell |
| [`internal/compat/gobimport`](internal/compat/gobimport) | Legacy gob snapshot and text WAL import into a disk database |

---

## Data model

The store is a **directed property graph**.

```
Node { ID uint64, Labels []string, Props map[propKeyID]value.Value }
Edge { ID uint64, From uint64, To uint64, Label string, Props map[propKeyID]value.Value }
```

- IDs are assigned by the engine, starting at `1`, and never reused. Counters live in the catalog keyspace.
- A node has a set of labels (sorted, unique, possibly empty). `Label()` returns the first label so the legacy one-label language keeps working. An edge has exactly one type, still stored in `Label`.
- Edges are directed (`From → To`). Multiple edges between the same pair are allowed.
- Deleting a node **cascades**: every incident inbound and outbound edge is removed.
- Property names are interned to `uint32` ids. Values are the typed model in [`docs/spec/values.md`](docs/spec/values.md): null, bool, int64, float64, string, bytes, list, map, date, datetime, duration, and node/edge references. A path is a result value and cannot be stored as a property.
- The legacy query language still writes only strings, integers, floats, and booleans. Callers always receive **copies**. Mutating a map from `Properties()` does not change the store.

### Storage layout

Without `BEGIN`, each `Graph` method auto-commits one transaction. The shell runs those transactions on the memory engine through `internal/txn`: a reader sees the committed snapshot from its start, plus its own writes, and a second writer of the same key gets a retryable conflict. `graph.New` in tests still uses the raw memory engine, where the last writer of a key wins.

Keys are split by keyspace. The memory engine keeps a sorted key slice and a value map for each one. The disk engine (`OpenEngine`) keeps one B+tree per keyspace ([ADR 0005](docs/adr/0005-btree.md)) on the page file from [ADR 0004](docs/adr/0004-page-file.md): 8 KiB pages by default, a CRC32C trailer, and a freelist ([page spec](docs/spec/pages.md)). A commit logs those pages to the binary WAL and syncs before it acknowledges ([ADR 0006](docs/adr/0006-wal.md), [ADR 0007](docs/adr/0007-disk-engine.md), [engine spec](docs/spec/engine.md)). The shell runs on the memory engine through the transaction manager ([ADR 0009](docs/adr/0009-mvcc.md), [transaction spec](docs/spec/transactions.md)). `graphdb migrate` is what writes a disk directory from an old gob snapshot.

```
N  node id            → labels and properties
E  edge id            → from, to, type, properties
O  from|type|to|edge  → outgoing adjacency
I  to|type|from|edge  → incoming adjacency
L  label id|node id   → label index
T  type id|edge id    → edge-type index
P  prop id|value|node → node property equality index
C  kind|name          → names, ids, and id counters
```

Neighbor lookup walks the outgoing adjacency of a node. `MATCH <label>` uses the label index. Equality `WHERE` on nodes can use the property index; range comparisons (`>`, `<`, …) scan the label set and compare in the executor. Property-key ids and the next node and edge ids live in the catalog keyspace.

### Traversals

| Operation | Algorithm | Notes |
|-----------|-----------|--------|
| `NEIGHBORS id DEPTH n` | BFS on outgoing edges | Start node omitted; depth must be ≥ 1 |
| `PATH from TO to` | Unweighted BFS | Empty result means no path; `from == to` returns that node |
| DFS | Preorder on outgoing edges | Exposed on the engine (`Graph.DFS`); not a query keyword |

---

## Query pipeline

The shell reads **one line** at a time. A GQL-lite script may still put several statements on that line, separated by `;`. Keywords are case-insensitive (`match`, `MATCH`, and `Match` are the same).

```
source line
    → compat/legacy   GQL-lite as written, or a rewritten legacy line
    → lang/parser     GQL-lite AST
    → lang/plan       logical plan
    → exec.Run        Volcano iterators → exec.Result
    → REPL            table of columns and rows
```

A rewritten legacy line attaches a `DeprecatedSyntax` notice. `HELP` and `EXIT` are shell commands. `SAVE` and `LOAD` still parse, and the error text is: use `graphdb backup`. That command is Phase 8. Until then, `graphdb migrate` is how an old snapshot becomes a disk database.

---

## Query language

The shell runs GQL-lite grammar version 1, specified in [`docs/spec/query-language.md`](docs/spec/query-language.md). The line language below is still accepted and rewritten to that AST.

### Values and properties

Property maps are `{key: value, key: value}`. Keys are identifiers. Values:

| Kind | Example |
|------|---------|
| String | `"Alice"` |
| Integer | `30` |
| Float | `1.5` |
| Boolean | `true` / `false` |
| Bare ident | treated as a string (`Paris`) |

`WHERE` operators: `=` `!=` `>` `<` `>=` `<=`. Comparison uses the typed rules in [`docs/spec/values.md`](docs/spec/values.md): integers and floats compare exactly (including `1` and `1.0`), booleans only allow `=` / `!=`, and a comparison that is null or that mixes incomparable kinds does not match the row. Missing properties do not match either.

### Commands

#### Create

```
CREATE NODE <label> {key: value, ...}
CREATE EDGE <fromId> -<LABEL>-> <toId> {key: value, ...}
```

Both endpoints of an edge must already exist. A write with no `RETURN` prints a summary:

```
created 1 node
created 1 edge
```

#### Read

```
GET NODE <id>
GET EDGE <id>
MATCH <nodeLabel> [WHERE <key> <op> <value>]
MATCH EDGE <edgeLabel> [WHERE <key> <op> <value>]
EDGES <fromId> TO <toId>
NEIGHBORS <id> [DEPTH <n>]     # default DEPTH 1
PATH <fromId> TO <toId>
SHOW STATS
```

- `GET` looks up a single record by ID.
- `MATCH` lists nodes with that label; `MATCH EDGE` lists edges with that relationship type.
- `EDGES 1 TO 2` lists **directed** edges from node 1 to node 2 (not the reverse).
- `PATH` is the unweighted shortest path as a node sequence.

#### Update and delete

```
UPDATE NODE <id> {key: value, ...}
UPDATE EDGE <id> {key: value, ...}
DELETE NODE <id>
DELETE EDGE <id>
```

`UPDATE` merges properties (existing keys overwritten, new keys added). `DELETE NODE` removes incident edges.

#### Transactions

```
BEGIN
BEGIN READ ONLY
COMMIT
ROLLBACK
VACUUM
```

`BEGIN` holds later statements in one transaction until `COMMIT` or `ROLLBACK`. `BEGIN READ ONLY` rejects writes. Without `BEGIN`, each statement commits on its own. `VACUUM` drops old versions no open snapshot can still read.

#### Persistence and session

```
HELP
EXIT            # also QUIT
```

`SAVE <file>` and `LOAD <file>` are still accepted by the parser. They fail, and the error text is: use `graphdb backup`.

### Example session

```
graph> CREATE NODE person {name: "Alice", age: 30}
created 1 node
graph> CREATE NODE person {name: "Bob", age: 20}
created 1 node
graph> CREATE EDGE 1 -KNOWS-> 2 {since: 2020}
created 1 edge
graph> GET NODE 1
n
1:person {age=30 name="Alice"}
graph> MATCH person WHERE age > 25
n
1:person {age=30 name="Alice"}
graph> MATCH EDGE KNOWS WHERE since >= 2020
e
1 1 -KNOWS-> 2 {since=2020}
graph> EDGES 1 TO 2
e
1 1 -KNOWS-> 2 {since=2020}
graph> PATH 1 TO 2
shortestPath(a, b)
path(node(1), edge(1), node(2))
graph> SHOW STATS
nodes:  2
edges:  1
labels: person
```

---

## Persistence

The shell does not write a snapshot. The disk engine is a directory: `db` is the heap and `wal/` is the binary log ([ADR 0007](docs/adr/0007-disk-engine.md)).

**Legacy import**

```bash
graphdb migrate --from-legacy data/graph.db --wal data/graph.wal --to data/db
```

The snapshot is the old gob file (version 0 or 1). The text WAL is optional query lines since that snapshot. The command replays them and writes a new disk directory. A newer gob version is rejected. Details are in [spec/legacy.md](docs/spec/legacy.md).

---

## REPL

`repl.Run` owns session lifetime:

1. Construct an empty in-memory `graph.Graph`.
2. Read-eval-print until `EXIT` or EOF.

On a TTY, `liner` provides history (persisted to `--history`) and keyword completion (GQL-lite and legacy). On a pipe or in tests, `bufio.Scanner` is used so scripts stay deterministic.

Results are aligned with `text/tabwriter`. A node or edge cell is expanded from the graph. `SHOW STATS` still prints `nodes:` / `edges:` / `labels:`.

---

## Tests

```bash
make test          # go test -race -count=1 ./...
make lint          # golangci-lint
```

| Package | What is covered |
|---------|-----------------|
| `internal/storage/memory` | Engine conformance: order, cursors, isolation, crash hooks |
| `internal/storage/graphstore` | Cascade delete, id allocation, property index |
| `internal/graph` | CRUD, cascade delete, indexes, export/import, BFS/DFS, shortest path |
| `internal/query` | Table-driven parser, execute, `WHERE`, GET/MATCH/EDGES, `BEGIN`/`COMMIT`, SAVE/LOAD rejection |
| `internal/compat/legacy` | Rewrite table from the spec, then `exec.Run` |
| `internal/txn` | Snapshot isolation, conflicts, vacuum, concurrent readers, disk reopen |
| `test/queries` | 235 GQL-lite goldens on the memory engine and the disk engine |
| `test/fuzz` | Parser and executor fuzz seeds |
| `test/consistency` | 10000 randomized snapshot-isolation histories |
| `internal/compat/gobimport` | Gob version 0 and 1, text WAL replay, into a disk directory |
| `internal/repl` | End-to-end session over a fake stdin |

There are no external services. All tests use `t.TempDir()` for files.

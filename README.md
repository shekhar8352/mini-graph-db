# mini-graph-db

An embedded, in-memory **property-graph database** written in Go. It stores labeled nodes and directed labeled edges, each with key-value properties, and is driven by an interactive REPL and a small line-based query language (not full Cypher).

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
                    │           internal/query            │
                    │  lexer → parser → AST → executor    │
                    └─────────────────┬───────────────────┘
                                      ▼
                    ┌─────────────────────────────────────┐
                    │  internal/graph → graphstore        │
                    │  shell: storage/memory              │
                    │  migrate: storage/disk.OpenEngine   │
                    └─────────────────────────────────────┘
```

Request path for a typical command:

1. The REPL reads a line (`graph> MATCH person WHERE age > 25`).
2. `query.Parse` lexes tokens and builds an AST (`MatchStmt`).
3. `query.Executor` calls the graph facade, which runs the statement as one transaction on the in-memory storage engine (`NodesByLabel` + `WHERE` compare).
4. The REPL prints a text table (nodes, edges, neighbors, path, or stats).

Packages depend inward only: `cmd` → `repl` → `query` → `graph` → `graphstore` → `storage`. `graphdb migrate` also calls `internal/compat/gobimport`, which writes `storage/disk`. There are no third-party dependencies in the engine or parser. The REPL uses `github.com/peterh/liner` solely for terminal line editing.

### Package map

| Path | Role |
|------|------|
| [`cmd/graphdb`](cmd/graphdb) | Process entry: cobra CLI (`shell`, `migrate`, `version`) |
| [`internal/repl`](internal/repl/repl.go) | Prompt, history, table-formatted output |
| [`internal/query`](internal/query) | Lexer, recursive-descent parser, AST, executor |
| [`internal/value`](internal/value) | Typed values, ordering, key and record encodings |
| [`internal/storage`](internal/storage) | Key-value engine interface |
| [`internal/storage/memory`](internal/storage/memory) | In-memory engine (sorted keys per keyspace) |
| [`internal/storage/disk`](internal/storage/disk) | Page file, buffer pool, B+tree, and `OpenEngine` (not used by the shell yet) |
| [`internal/wal`](internal/wal) | Binary write-ahead log used by the disk engine |
| [`internal/storage/graphstore`](internal/storage/graphstore) | Nodes, edges, adjacency, indexes, catalog ids |
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

Each `Graph` method auto-commits one transaction on the in-memory engine. A transaction sees the committed snapshot from its start, plus its own writes. Other transactions do not see those writes until commit.

Keys are split by keyspace. The memory engine keeps a sorted key slice and a value map for each one. The disk engine (`OpenEngine`) keeps one B+tree per keyspace ([ADR 0005](docs/adr/0005-btree.md)) on the page file from [ADR 0004](docs/adr/0004-page-file.md): 8 KiB pages by default, a CRC32C trailer, and a freelist ([page spec](docs/spec/pages.md)). A commit logs those pages to the binary WAL and syncs before it acknowledges ([ADR 0006](docs/adr/0006-wal.md), [ADR 0007](docs/adr/0007-disk-engine.md), [engine spec](docs/spec/engine.md)). The shell still runs on the memory engine. `graphdb migrate` is what writes a disk directory from an old gob snapshot.

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

Each statement is **one line**. Keywords are case-insensitive (`match`, `MATCH`, and `Match` are the same).

```
source line
    → Lexer     identifiers, numbers, "strings", { } : , - ->  = != > < >= <=
    → Parser    recursive descent → Stmt (AST)
    → Executor  graph calls → Result
    → REPL      tables or a one-line message
```

Lexer tokens include identifiers, integers/floats, quoted strings (`\"`, `\\`, `\n`, `\t`), braces, commas, the edge arrow form `-LABEL->` (minus, identifier, `->`), and comparison operators.

The parser produces typed statements (`CreateNodeStmt`, `MatchEdgeStmt`, `GetNodeStmt`, …). Unknown commands fail at parse time. The executor never re-parses: it switches on the AST.

Each statement auto-commits one transaction on the in-memory engine. `SAVE` and `LOAD` still parse, and the error text is: use `graphdb backup`. That command is Phase 8. Until then, `graphdb migrate` is how an old snapshot becomes a disk database.

---

## Query language

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

Both endpoints of an edge must already exist. The engine prints the new ID:

```
created node 1 label=person
created edge 1 1 -KNOWS-> 2
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

#### Persistence and session

```
HELP
EXIT            # also QUIT
```

`SAVE <file>` and `LOAD <file>` are still accepted by the parser. They fail, and the error text is: use `graphdb backup`.

### Example session

```
graph> CREATE NODE person {name: "Alice", age: 30}
created node 1 label=person
graph> CREATE NODE person {name: "Bob", age: 20}
created node 2 label=person
graph> CREATE EDGE 1 -KNOWS-> 2 {since: 2020}
created edge 1 1 -KNOWS-> 2
graph> GET NODE 1
ID  LABEL   PROPS
1   person  age=30 name="Alice"
graph> MATCH person WHERE age > 25
ID  LABEL   PROPS
1   person  age=30 name="Alice"
graph> MATCH EDGE KNOWS WHERE since >= 2020
ID  FROM  TO  LABEL  PROPS
1   1     2   KNOWS  since=2020
graph> EDGES 1 TO 2
ID  FROM  TO  LABEL  PROPS
1   1     2   KNOWS  since=2020
graph> PATH 1 TO 2
ID  LABEL   PROPS
1   person  age=30 name="Alice"
2   person  age=20 name="Bob"
path 1 -> 2
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

On a TTY, `liner` provides history (persisted to `--history`) and keyword completion (`CREATE NODE`, `MATCH EDGE`, `GET NODE`, …). On a pipe or in tests, `bufio.Scanner` is used so scripts stay deterministic.

Results are aligned with `text/tabwriter`:

- nodes: `ID LABEL PROPS`
- edges: `ID FROM TO LABEL PROPS`
- neighbors: `DEPTH ID LABEL PROPS`

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
| `internal/query` | Table-driven parser, execute, `WHERE`, GET/MATCH/EDGES, SAVE/LOAD rejection |
| `internal/compat/gobimport` | Gob version 0 and 1, text WAL replay, into a disk directory |
| `internal/repl` | End-to-end session over a fake stdin |

There are no external services. All tests use `t.TempDir()` for files.

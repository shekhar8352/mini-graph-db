# mini-graph-db

An embedded, in-memory **property-graph database** written in Go. It stores labeled nodes and directed labeled edges, each with key-value properties, and is driven by an interactive REPL and a small line-based query language (not full Cypher).

The working set lives in RAM. Durability is optional: a gob snapshot plus an append-only write-ahead log (WAL). Mutating statements are logged; on the next start the last snapshot is loaded and the WAL is replayed.

```bash
go test ./...
go run ./cmd/graphdb
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-db` | `graph.db` | Snapshot file loaded on startup |
| `-wal` | `graph.wal` | Append-only log of mutating statements |
| `-history` | `graph.history` | REPL command history (up-arrow) |

In a real terminal the prompt supports line editing: left/right move the cursor, up/down walk history, Tab completes keywords, Ctrl-C cancels the current line, Ctrl-D or `EXIT` leaves the REPL. Piped input uses a plain scanner (no line editor).

---

## Architecture

```
                    ┌─────────────────────────────────────┐
                    │            cmd/graphdb              │
                    │         flags: -db -wal -history    │
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

Packages depend inward only: `cmd` → `repl` → `query` → (`graph`, `persist`) → `graph`. There are no third-party dependencies in the engine, parser, or persistence layer. The REPL uses `github.com/peterh/liner` solely for terminal line editing.

### Package map

| Path | Role |
|------|------|
| [`cmd/graphdb`](cmd/graphdb/main.go) | Process entry: parse flags, start the REPL |
| [`internal/repl`](internal/repl/repl.go) | Prompt, recovery, history, table-formatted output |
| [`internal/query`](internal/query) | Lexer, recursive-descent parser, AST, executor |
| [`internal/graph`](internal/graph) | Property graph, CRUD, indexes, BFS/DFS, shortest path |
| [`internal/persist`](internal/persist) | Gob snapshot encode/decode and WAL |

---

## Data model

The store is a **directed property graph**.

```
Node { ID uint64, Label string, Props map[string]any }
Edge { ID uint64, From uint64, To uint64, Label string, Props map[string]any }
```

- IDs are assigned by the engine, starting at `1`, and never reused within a process lifetime (counters are persisted in the snapshot).
- Edges are directed (`From → To`). Multiple edges between the same pair are allowed.
- Deleting a node **cascades**: every incident inbound and outbound edge is removed.
- Property values in the query language are strings, integers (`int64`), floats (`float64`), or booleans.
- Callers always receive **copies**. Mutating a returned `Props` map does not change the store.

### In-memory layout

`Graph` is guarded by a `sync.RWMutex`. Traversals take a read lock; writes take a write lock.

```
nodes      map[id] *Node
edges      map[id] *Edge
outEdges   map[nodeID] []edgeID     // adjacency, outgoing
inEdges    map[nodeID] []edgeID     // adjacency, incoming
labelIndex map[label] set[nodeID]   // MATCH person
propIndex  map[key][type:value] set[nodeID]  // MATCH … WHERE name = "Alice"
nextNode, nextEdge                  // ID allocators
```

Neighbor lookup is O(degree) via the adjacency lists. `MATCH <label>` uses the label index. Equality `WHERE` on nodes can use the property index; range comparisons (`>`, `<`, …) scan the label set and compare in the executor.

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
    → Executor  Graph / persist calls → Result
    → REPL      tables or a one-line message
```

Lexer tokens include identifiers, integers/floats, quoted strings (`\"`, `\\`, `\n`, `\t`), braces, commas, the edge arrow form `-LABEL->` (minus, identifier, `->`), and comparison operators.

The parser produces typed statements (`CreateNodeStmt`, `MatchEdgeStmt`, `GetNodeStmt`, …). Unknown commands fail at parse time. The executor never re-parses: it switches on the AST.

Mutating statements (`CREATE`, `UPDATE`, `DELETE`) are WAL-logged after a successful execute. `SAVE` / `LOAD` / reads are not logged. WAL replay calls `ExecString(line, replay=true)` so recovered statements are not appended again.

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

`WHERE` operators: `=` `!=` `>` `<` `>=` `<=`. Numbers compare numerically; booleans only allow `=` / `!=`; everything else compares as strings.

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
SAVE <file>     # gob snapshot; truncates the WAL
LOAD <file>     # replace in-memory graph; truncates the WAL
HELP
EXIT            # also QUIT
```

Unquoted paths may contain dots and slashes (`SAVE graph.db`, `SAVE data/g.db`). Quoted paths work too (`LOAD "my graph.db"`).

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
1   person  age=30 name=Alice
graph> MATCH person WHERE age > 25
ID  LABEL   PROPS
1   person  age=30 name=Alice
graph> MATCH EDGE KNOWS WHERE since >= 2020
ID  FROM  TO  LABEL  PROPS
1   1     2   KNOWS  since=2020
graph> EDGES 1 TO 2
ID  FROM  TO  LABEL  PROPS
1   1     2   KNOWS  since=2020
graph> PATH 1 TO 2
ID  LABEL   PROPS
1   person  age=30 name=Alice
2   person  age=20 name=Bob
path 1 -> 2
graph> SHOW STATS
nodes:  2
edges:  1
labels: person
```

---

## Persistence

Durability is two files, not a page store.

**Snapshot (`-db`, `SAVE` / `LOAD`)**  
Full copy of nodes, edges, and ID counters, encoded with `encoding/gob`. Properties are stored as a typed DTO (`string` / `int64` / `float64` / `bool`) so `map[string]any` round-trips cleanly. `Graph.Import` rebuilds adjacency lists and indexes from the snapshot.

**WAL (`-wal`)**  
One mutating query line per record, flushed with `Sync`. Recovery:

1. If `graph.db` exists, load it.
2. Replay every statement in `graph.wal` (without re-appending).
3. New mutations append to the WAL until the next `SAVE`, which writes a snapshot and truncates the log.

`LOAD` also truncates the WAL so the restored graph is the new source of truth.

This is the usual “checkpoint + redo log” pattern in miniature: the snapshot is a consistent checkpoint; the WAL is the redo stream since that checkpoint.

---

## REPL

`repl.Run` owns session lifetime:

1. Construct an empty `graph.Graph`.
2. Load snapshot if the `-db` path exists.
3. Open the WAL, replay it, attach it to the executor.
4. Read-eval-print until `EXIT` or EOF.

On a TTY, `liner` provides history (persisted to `-history`) and keyword completion (`CREATE NODE`, `MATCH EDGE`, `GET NODE`, …). On a pipe or in tests, `bufio.Scanner` is used so scripts stay deterministic.

Results are aligned with `text/tabwriter`:

- nodes: `ID LABEL PROPS`
- edges: `ID FROM TO LABEL PROPS`
- neighbors: `DEPTH ID LABEL PROPS`

---

## Tests

```bash
go test ./...
```

| Package | What is covered |
|---------|-----------------|
| `internal/graph` | CRUD, cascade delete, indexes, export/import, BFS/DFS, shortest path |
| `internal/query` | Table-driven parser, execute, `WHERE`, GET/MATCH/EDGES, SAVE/LOAD, WAL replay |
| `internal/persist` | Gob round-trip of mixed property types; WAL append / read / truncate |
| `internal/repl` | End-to-end session over a fake stdin; WAL recovery across two runs |

There are no external services. All tests use `t.TempDir()` for files.

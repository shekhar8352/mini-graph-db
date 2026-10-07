# Changelog

All notable work on this repository is recorded here. Task IDs match [ROADMAP.md](../ROADMAP.md).

## Unreleased

### Phase 4.3 — GQL-lite AST and printer (2026-10-07)

- **4.3** `internal/lang/ast` is the typed tree for grammar version 1. Nodes do not store positions. `Walk` and `Rewrite` visit that tree. `ast.Format` prints it, and `Parse(Format(Parse(x)))` matches `Parse(x)`.
- `internal/lang/parser` is the recursive-descent parser the round-trip uses. It stops at the first syntax error. The message is `expected … at line:col`. Error recovery and `docs/spec/examples` stay in task 4.4.
- Keyword tokens keep the spelling from the source. `!=` is stored as `<>`. `UNION DISTINCT` is stored as `UNION`. A variable-length `*` and `*1..` are the same open range. The shell still runs the legacy line language.

#### Policy compliance

| Policy | This task |
|--------|-----------|
| P10 Change management | Grammar version stays 1. `CALL` may end with `WHERE`, which example E43 already used. `REQUIRE`, `ALTER`, and `ONLY` stay unreserved and are matched case-insensitively. |
| P12 Testing | Round-trip corpus, visitor tests, syntax errors, and `FuzzParse` run under `go test -race`. |
| P13 Documentation | Spec note for `CALL` … `WHERE` and for the three unreserved grammar words. |
| P1–P9, P11 | No runtime change to storage, transactions, or the shell. |

### Phase 4.2 — GQL-lite lexer (2026-10-07)

- **4.2** `internal/lang/lexer` tokenizes grammar version 1: identifiers, backtick names, Go string escapes, decimal and hex integers, float exponents, `$name` parameters, operators, and `--` / `/* */` comments. Syntax errors are `Syntax` and include `at line:col`. `date`, `datetime`, and `duration` are identifiers in front of a call. A reserved word stays a keyword after `.` and `:`.

Adjacent `--` is a line comment. `-->` is still minus and then `->`, and `<--` is `<-` and then minus. The undirected pattern is written `-[]-` or `- -`. [spec/query-language.md](spec/query-language.md) says so. Grammar version stays 1. The shell still runs the legacy line language.

#### Policy compliance

| Policy | This task |
|--------|-----------|
| P10 Change management | Grammar version stays 1. The `--` sentence in the spec matched the comment rule and the `-->` operator rule at the same time; the spec now states which one wins. |
| P12 Testing | Lexer tests and `FuzzScan` run under `go test -race`. |
| P13 Documentation | Spec clarification in the lexical and pattern sections. |
| P1–P9, P11 | No runtime change to storage, transactions, or the shell. |

### Phase 4.1 — GQL-lite grammar (2026-10-07)

- **4.1** [spec/query-language.md](spec/query-language.md) is grammar version 1: lexical rules, statement grammar, expression precedence, scope, patterns, mutation, functions, procedures, DDL, DCL, errors, the legacy rewrite table, and 60 examples. [ADR 0010](adr/0010-gql-lite.md) accepts it. The shell still runs the legacy line language. No runtime change.

#### Policy compliance

| Policy | This task |
|--------|-----------|
| P2 Atomicity & isolation | The spec keeps snapshot isolation, statement-level rollback inside an open transaction, and first-committer-wins. `MERGE` does not take a row lock. |
| P3 Consistency | `DELETE` of a node that still has edges is `ConstraintViolation`. `DETACH DELETE` removes those edges. Unique, existence, and type constraints are in the grammar; enforcement is Phase 5. |
| P9 Resource governance | Variable-length walks stop at `max_pattern_hops` (default 128) with `ResourceExhausted`. |
| P10 Change management | Query grammar version is 1. A later incompatible change bumps that version. Page, WAL, and MVCC formats are unchanged. |
| P13 Documentation | The spec and ADR 0010. |
| P1, P4–P8, P11, P12 | No code change. Durability, checksums, recovery, auth, and tests are unchanged. |

### Phase 3 — Transactions and MVCC (2026-10-03)

- **3.1–3.3** `internal/txn` assigns a snapshot at begin and a commit timestamp at commit. Writes stay private until commit. First-committer-wins returns retryable `Conflict` and leaves the transaction open. The versions and the oracle are one underlying storage commit. On disk that commit is still page images, `wal.Sync`, then publish. `TxnCommit` stays an empty payload.
- **3.4** A crash before the inner `TxnCommit` leaves none of the write set visible. A crash after it leaves all of it. `Rollback` of a transaction that wrote appends `TxnAbort` on the disk engine. Recovery already ignores a transaction that never committed.
- **3.5** A lost update is `Conflict`. Write skew commits on both sides. `InsertUnique` that loses the key is `ConstraintViolation` and is not retryable.
- **3.6** `VACUUM` and `Manager.Vacuum` drop versions no open snapshot can read. `MVCCStats` exposes `versions_reclaimed_total` and `oldest_snapshot_age_seconds`. Background collection runs only when `GCInterval` is set. The shell leaves it off.
- **3.7** `BEGIN`, `BEGIN READ ONLY`, `COMMIT`, `ROLLBACK`, and `VACUUM` parse in the legacy grammar. Without `BEGIN`, each statement auto-commits. The shell opens the memory engine through the manager.
- **3.8** Concurrent writers and readers keep edge endpoints, a monotonic counter, and a stable snapshot across statements in one transaction.
- **3.9** No transaction holds a row lock across statements, so two transactions cannot deadlock waiting for each other's keys.

Page and WAL format versions stay 1. The MVCC stamp is format byte 1 on the oracle record in keyspace `V`. A newer byte is refused. Raw values from before this phase are wrapped as heads on open. `graph.New` stays on the raw memory engine.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P1 Durability | A manager commit is one storage commit. On disk, `Commit` still returns only after `wal.Sync` and the heap publish. The memory engine has no fsync. There is no relaxed sync mode. |
| P2 Atomicity & isolation | Snapshot isolation. A conflict aborts neither transaction until the caller rolls the loser back; the error is retryable `Conflict`. The write set is applied all at once or not at all. Write skew is allowed. |
| P3 Consistency | `InsertUnique` is checked at commit. A phantom or an existing live key is `ConstraintViolation`. Declared schema constraints are Phase 5. Cascade delete stays in graphstore. |
| P4 Data integrity | Page and WAL checksums are unchanged. A truncated version-chain record is `Corruption`. |
| P5 Recoverability | Open recovers the oracle as the last committed timestamp. A transaction without `TxnCommit` is not installed. A crash during the inner commit does not publish a prefix of the write set. |
| P9 Resource governance | `MaxOpenTxns` (default 1024), `MaxWriteSet` (default 100000), and `IdleTimeout` (default off) return `ResourceExhausted` instead of growing without a bound. |
| P10 Change management | Page and WAL versions stay 1. The oracle format byte is 1. A newer byte is `InvalidArgument` before the data is used. Pre-manager values are migrated in place on open. |
| P12 Testing | Conformance on the manager, lost update, write skew, unique phantom, vacuum, limits, migration, abort record, concurrent graph, disk reopen, a crash cut after `wal.Sync`, reader latency under a bulk writer, and 10000 snapshot-isolation histories run under `go test -race`. |
| P13 Documentation | [ADR 0009](adr/0009-mvcc.md) and [spec/transactions.md](spec/transactions.md). |
| P6–P8, P11 | Authentication, audit, Prometheus, and replicas are later phases. The version counters are in-process until Phase 8. |

### Phase 2F — Migration from legacy formats (2026-10-02)

- **2F.1** `internal/compat/gobimport` reads a gob snapshot (version 0 or 1) and an optional text WAL of query lines, replays those lines, and writes the result with one commit into a new disk database directory.
- **2F.2** `graphdb migrate --from-legacy <snapshot> [--wal <file>] --to <data-dir>` is that import. The destination must not already contain `db`.
- **2F.3** `internal/persist` is removed. The shell no longer loads a snapshot or appends a text log. `SAVE` and `LOAD` still parse, and the error text is: use `graphdb backup`. `--db` and `--wal` are no longer shell flags.

The shell stays on the memory engine for the session. Format version of the disk heap and the binary WAL stays 1. `graphdb backup` is Phase 8.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P1 Durability | The migrated graph is one disk commit. `Commit` still returns only after the WAL sync and the heap publish. The shell itself does not fsync a snapshot. |
| P5 Recoverability | Reopening the migrated directory replays from the checkpoint written on close. A crash before that commit does not leave a new `db` when migrate created the directory. |
| P10 Change management | Legacy snapshot versions 0 and 1 still load. A newer gob version is refused with an explicit error. The disk format version stays 1. |
| P12 Testing | Version 0, version 1, WAL replay, a newer-version refusal, an existing destination, a bad WAL line, and the migrate command run under `go test -race`. |
| P13 Documentation | [ADR 0008](adr/0008-legacy-import.md) and [spec/legacy.md](spec/legacy.md). |
| P2–P4, P6–P9, P11 | Unchanged from Phase 2E. Constraints, authentication, and backup are later phases. |

### Phase 2E — Disk engine assembly and recovery (2026-10-02)

- **2E.1** `storage/disk.OpenEngine` implements `storage.Engine`. A commit logs page images (`TxnBegin`, `PageWrite`, `TxnCommit`), syncs the WAL, and only then writes those pages to the heap. Callers do not see the commit until that sync returns. Snapshot reads use an in-memory undo log over the latest tree.
- **2E.2** A background checkpointer runs on a 30s interval and after 64 MiB of WAL. It flushes dirty pages, records the durable LSN in the heap header, and deletes sealed segments that end at or before that LSN. The active segment stays. Remaining segment numbers may start above 1.
- **2E.3** Open replays committed page images from the checkpoint LSN. A newer format version is refused before the checksum. Read-only open rejects a missing heap file and rejects read-write transactions. Replay may still write repaired pages.
- **2E.4** The disk engine passes `storage/enginetest`. Crash tests cut the WAL at 1000 offsets: a torn tail recovers a prefix of acknowledged commits, and a bad checksum is `Corruption`.
- **2E.5** Sequential insert, random point read, range scan, and adjacency fan-out baselines are in [test/bench/BASELINES.md](../test/bench/BASELINES.md).

The shell still uses the memory engine and the text WAL. Format version stays 1. Legacy gob import is Phase 2F.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P1 Durability | `Commit` returns nil only after `wal.Sync` and the heap publish. A crash before that sync drops the transaction. A crash after it is repaired by replaying page images. There is no relaxed sync mode. |
| P2 Consistency | A transaction sees the snapshot from `Begin` plus its own writes. Uncommitted writes stay invisible. The last writer of a key wins. First-committer-wins is Phase 3. |
| P4 Data integrity | Page and WAL checksums stay CRC32C. A bad checksum on a complete record or page is `Corruption`. A torn WAL tail is truncated and is not returned. |
| P5 Recoverability | Open replays from the checkpoint LSN and installs only transactions that have `TxnCommit`. Reinstalling an image is a no-op. Checkpoint truncation bounds how much log the next open reads. Backups and point-in-time restore wait on Phase 8. |
| P10 Change management | Heap and WAL format versions stay 1. A newer version is refused with an explicit error before the checksum is checked. |
| P12 Testing | `enginetest`, reopen, read-only, format, checksum, checkpoint, and 1000 WAL cut points run under `go test -race`. Baselines are recorded. |
| P13 Documentation | [ADR 0007](adr/0007-disk-engine.md) and [spec/engine.md](spec/engine.md). |
| P3, P6–P9, P11 | Not yet applicable. Constraints, authentication, and the server are later phases. |

### Phase 2D — Binary WAL (2026-09-27)

- **2D.1** `internal/wal` is a segmented redo log. Each record carries an LSN, a transaction id, a type, a payload, and a CRC32C trailer. `Append` buffers. `Sync` fsyncs. A lone `Sync` does not wait; when other committers are already waiting, one fsync covers the batch after a 1 ms window. Segment files are `000000000001.wal` and up, 64 MiB by default. The reader checks the checksum and stops at a torn tail.
- **2D.2** `wal.Archiver` is called after a segment is sealed and synced. The hook is the extension point for WAL archiving. This phase does not copy segments.
- **2D.3** Opening a log truncates a torn tail on the last segment and does not return a partial record. A complete record with a bad checksum is corruption. Tests cut the file at every byte and kill the writer at random offsets; every record whose `Sync` returned nil is still there.

The shell still replays the text WAL in `internal/persist`. Applying these records to pages is Phase 2E. Format version of the binary log is 1, separate from the page file.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P1 Durability | A nil `Sync` means every earlier `Append` is on disk and fsynced. A crash before that `Sync` drops the buffered records. Acknowledged commits of the database still wait on Phase 2E to call this before it returns. |
| P4 Data integrity | Every segment header and every record ends with CRC32C. A bad checksum on a complete record is `Corruption`. A short tail is truncated instead of being returned. |
| P5 Recoverability | `Open` repairs a torn tail and then iterates only whole records. Replaying from a checkpoint, and deleting old segments, is Phase 2E. |
| P10 Change management | WAL format version is 1. A newer version is refused with an explicit error before the checksum is checked. |
| P12 Testing | Round-trip, rotation, archive retry, group commit, every-byte torn tails, and 100 killed writers run under `go test -race`. |
| P13 Documentation | [ADR 0006](adr/0006-wal.md) and [spec/wal.md](spec/wal.md). |
| P2, P3, P6–P9, P11 | Not yet applicable. Transactions, constraints, and the server are later phases. |

### Phase 2C — B+tree (2026-09-26)

- **2C.1** Leaf and internal pages are slotted. Cell bytes grow up from a fixed header and a directory of uint16 offsets grows down from the end of the payload. Keys are stored in full. There is no prefix compression.
- **2C.2** `storage/disk.Tree` is one B+tree per keyspace, rooted at that keyspace's header slot. `Put` inserts or replaces and splits a full page. `Delete` removes a key and borrows or merges an underfull page. `Get` is a point lookup. `Cursor` seeks the first key greater than or equal to a target, or the last key less than or equal to one, and walks forward and backward.
- **2C.3** A value longer than a quarter of the page is stored on a chain of overflow pages. The leaf cell keeps the value length and the first page id.
- **2C.4** A million random puts, deletes, and gets match a reference map. Concurrent readers share the tree with one writer.

The shell still uses the memory engine. This phase does not log tree updates. A crash during a multi-page split is not atomic; durability of a committed update waits on the WAL from Phase 2D and the disk engine in Phase 2E. Format version stays 1.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P4 Data integrity | Leaf and internal decoders reject a truncated cell, a bad offset, an empty child, or a short overflow chain as `Corruption`. Tests check key order, separators, sibling links, and a single leaf depth. |
| P10 Change management | Page format version stays 1. Types 4–6 were reserved in Phase 2B and are now the leaf, internal, and overflow layouts. |
| P12 Testing | Page round-trip, split and merge, overflow, two keyspaces, a 1M-op comparison with a map, and concurrent readers run under `go test -race`. |
| P13 Documentation | [ADR 0005](adr/0005-btree.md) and the B+tree section of [spec/pages.md](spec/pages.md). |
| P1 Durability | Not yet. Overflow and split writes can leak a page on a crash. Acknowledged commits wait on the WAL. |
| P2, P3, P5–P9, P11 | Not yet applicable. Transactions, recovery, and the server are later phases. |

### Phase 2B — Page file and buffer pool (2026-09-25)

- **2B.1** `storage/disk.PageFile` creates and opens a heap file. Page 0 is the header (`GRDB`, format version 1, page size, database id, creation time, checkpoint LSN, freelist head, page count, and a root slot per keyspace). `ReadPage`, `WritePage`, `Allocate`, `Free`, `Sync`, and `Truncate` operate on the other pages. Each page ends with a CRC32C trailer. A newer format version is refused from the first 36 bytes, before the checksum is checked. The default page size is 8 KiB; the size is fixed at creation.
- **2B.2** `storage/disk.Pool` is a fixed frame table. `Get` pins a page, the last `Unpin` marks it most recently used, and eviction takes the least recently unpinned frame, writing it back when it is dirty. `FlushAll` writes the dirty list and syncs. Stats expose hits, misses, evictions, flushes, and the hit ratio. Hooks report the same events and must not call back into the pool.
- **2B.3** `storage/disk/fs` wraps `os.File`. `Fault` fails the next read, write, or sync, short-writes, or queues writes until sync. A queued write is visible to a later read. `Discard` drops the queue.

The shell still uses the memory engine. There is no binary WAL yet.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P1 Durability | `Sync` and `Close` fsync the file. A crash during allocate or free can leak a page; the write order does not make a live page look free. Acknowledged database commits wait on the WAL, which is Phase 2D/2E. |
| P4 Data integrity | Every page carries CRC32C. A bad checksum, a short page, a page id that does not match its position, or a freelist that does not match the header is `Corruption`. |
| P5 Recoverability | Open ignores a torn tail past the published page count and refuses a file that is shorter than that count. Redo from a checkpoint is Phase 2E. |
| P10 Change management | Page format version is 1. A newer version is refused with an explicit error. Page size cannot change after creation. Reserved page types 4–6 do not bump the version. |
| P12 Testing | Page-file, freelist, truncate, checksum, buffer-pool, and fault-injection tests run under `go test -race`. |
| P13 Documentation | [ADR 0004](adr/0004-page-file.md) and [spec/pages.md](spec/pages.md). |
| P2, P3, P6–P9, P11 | Not yet applicable. Transactions, constraints, and the server are later phases. |

### Phase 2A — Storage interface and in-memory engine (2026-09-25)

- **2A.1** `storage.Engine` and `storage.Tx` are a key-value API: begin read-only or read-write, get/put/delete, a cursor (`Seek`, `SeekReverse`, `Next`, `Prev`), commit, rollback, `Sync`, and `Stats`. Keyspaces are an argument, not a prefix inside the key.
- **2A.2** `storage/memory` keeps a sorted key slice and a value map per keyspace. A transaction reads the snapshot from `Begin` plus its own writes. Commit publishes those writes onto the latest snapshot. Last writer wins per key until Phase 3.
- **2A.3** `storage/enginetest` checks order, cursor positioning, isolation of uncommitted writes, and crash hooks (`BeforeCommit`, `AfterCommit`, `Crash`). The memory engine passes it.
- **2A.4** `storage/graphstore` stores nodes, edges, adjacency, label and property indexes, and catalog ids on a `storage.Tx`. Delete of a node removes incident edges. IDs come from catalog counters and are not reused. `internal/graph` is the facade the query executor, shell, and gob snapshot use, so those tests run on the memory engine.

Gob snapshots stay format version 1. There is no page file and no binary WAL yet.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P2 Consistency | One transaction can commit or roll back. Uncommitted writes are invisible to other transactions. Multi-statement transactions and write-write conflicts are Phase 3. |
| P10 Change management | No on-disk format change. Gob snapshot version 1 is unchanged. |
| P12 Testing | `storage/enginetest` is the conformance suite. Existing graph, query, persist, and shell tests run on the memory engine. |
| P13 Documentation | [ADR 0003](adr/0003-storage-engine.md). |
| P1, P4–P9, P11 | Not yet applicable. Durability, page checksums, and the disk engine are later Phase 2 tasks. |

### Phase 1 — Data model and typed value system (2026-09-22)

- **1.1** `internal/value.Value` is a tagged union: Null, Bool, Int, Float, String, Bytes, List, Map, Date, DateTime, Duration, Node, Edge, Path. Path is not storable as a property.
- **1.2** Comparisons are three-valued. `= null` is null; `IS NULL` / `IS NOT NULL` are the tests that return bool. `NOT` / `AND` / `OR` follow the same rules. `WHERE` keeps only true rows.
- **1.3** Total order is Null &lt; Bool &lt; Int/Float &lt; String &lt; Bytes &lt; Date &lt; DateTime &lt; Duration &lt; List &lt; Map &lt; Node &lt; Edge &lt; Path. `−0` equals `+0`. Every NaN equals every other NaN and sorts above `+Inf`. Int and float compare exactly, including past 2^53. Durations compare as `(months, days, nanos)`, not elapsed time.
- **1.4** `Equal` and `Hash` follow that order (null equals null; `1` equals `1.0`), for `DISTINCT`, `GROUP BY`, and unique constraints.
- **1.5** `EncodeKey` / `DecodeKey` are memcomparable. Numbers share one encoding (IEEE sign-bit order plus a gap offset for ints that are not exact floats). Strings escape a `0x00` terminator. `EncodeComposite` concatenates keys.
- **1.6** `EncodeRecord` / `DecodeRecord` is a versioned, kind-preserving varint encoding. Record version 1; a newer version is rejected. Float bits, including `−0` and NaN payloads, round-trip.
- **1.7** `Format` and `Parse` cover every kind, including `date(...)`, `datetime(...)`, and `duration(...)`.
- **1.8** Nodes store a sorted unique `[]string` of labels. `Label()` is the first label. The label index maps each label to the set of nodes that carry it.
- **1.9** Property names are interned to `uint32` in the in-memory engine. Properties are `value.Value`. Gob snapshots are format version 1 (label set, intern table, record-encoded properties). Version 0 snapshots still load; a newer version is rejected.
- **1.10** Fuzz tests cover record and key round-trips and key order. A 100k-pair test checks that key order matches `Compare`.

Legacy `WHERE` no longer coerces mismatched types through strings, and numeric comparison is exact (the old `1e-9` tolerance is gone). Shell property cells quote strings.

#### Policy compliance

| Policy | This phase |
|--------|------------|
| P3 Consistency | Types exist. Declared constraints are still Phase 5; the engine rejects unstorable path properties. |
| P10 Change management | Gob snapshot format is version 1. Version 0 still loads. Newer snapshots are rejected with an explicit error. Key and record encodings are versioned. |
| P12 Testing | `go test -race ./internal/value` covers round-trips, a 100k-pair order property, and fuzz seeds. |
| P13 Documentation | `docs/spec/values.md` and [ADR 0002](adr/0002-value-model.md). |
| P1, P2, P4–P9, P11 | Not yet applicable (no page store, transactions, or server). |

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

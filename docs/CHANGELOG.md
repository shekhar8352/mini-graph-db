# Changelog

All notable work on this repository is recorded here. Task IDs match [ROADMAP.md](../ROADMAP.md).

## Unreleased

### Phase 2C — B+tree (2026-09-26)

- **2C.1** Leaf and internal pages are slotted. Cell bytes grow up from a fixed header and a directory of uint16 offsets grows down from the end of the payload. Keys are stored in full. There is no prefix compression.
- **2C.2** `storage/disk.Tree` is one B+tree per keyspace, rooted at that keyspace's header slot. `Put` inserts or replaces and splits a full page. `Delete` removes a key and borrows or merges an underfull page. `Get` is a point lookup. `Cursor` seeks the first key greater than or equal to a target, or the last key less than or equal to one, and walks forward and backward.
- **2C.3** A value longer than a quarter of the page is stored on a chain of overflow pages. The leaf cell keeps the value length and the first page id.
- **2C.4** A million random puts, deletes, and gets match a reference map. Concurrent readers share the tree with one writer.

The shell still uses the memory engine. There is no binary WAL yet. A crash during a multi-page split is not atomic; durability of a committed update is Phase 2D and 2E. Format version stays 1.

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

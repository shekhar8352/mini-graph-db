# Disk engine

This is the durable `storage.Engine` in `internal/storage/disk`. Format version of the heap and the log stays 1. The decision record is [ADR 0007](../adr/0007-disk-engine.md). Page bytes are [spec/pages.md](pages.md). Log bytes are [spec/wal.md](wal.md).

`OpenEngine` opens a directory. The heap file is `db`. Segment files live in `wal/`. The shell does not open this directory. `graphdb migrate` writes one from a legacy gob snapshot ([spec/legacy.md](legacy.md)).

## Commit

A commit appends page images, syncs the log, and only then writes those pages to the heap. `Commit` returns nil after that sync and the publish. Callers do not see the writes until then.

The logged records are `TxnBegin`, one `PageWrite` per dirty page, and `TxnCommit`. Data pages are logged in page-id order. The header page is last. `TreeInsert` and `TreeDelete` are not written. A transaction that never reaches `TxnCommit` is not installed on recovery.

`BeforeCommit` runs first. An error leaves the transaction open. `AfterCommit` runs after the commit is durable and published. An error from that hook means the commit happened.

A read-only transaction and a transaction with no writes commit without logging.

## Snapshots

A transaction reads the committed state from its `Begin`, plus its own writes. The tree itself holds the latest commit. Older values live in an in-memory undo log until the oldest open transaction no longer needs them. Two writers of the same key both succeed; the last commit wins. Conflict detection is Phase 3.

## Recovery

Open reads the checkpoint LSN from the heap header and replays the log from there. It installs page images only for transactions that have `TxnCommit`. Installing the same image again does not change the page. The freelist is checked after replay.

If the checkpoint LSN is past the end of the log, open returns `Corruption`. A torn tail on the last segment is truncated by WAL open and is not returned as a record. A complete record with a bad checksum is `Corruption`. An unknown record type is `Corruption`.

A heap or log whose format version is newer than 1 is refused before the checksum is checked.

## Checkpoint

`Checkpoint` flushes dirty pages, appends a `Checkpoint` record, syncs the log, stores that durable LSN in the heap header, syncs the heap, and deletes sealed segments that end at or before that LSN. The active segment stays. A background loop does this every 30s, and when 64 MiB has been synced since the last checkpoint. `Close` on a healthy read-write engine checkpoints once. A read-only engine does not.

## Read-only

`EngineOptions.ReadOnly` rejects `Begin` of a read-write transaction. A missing `db` file is `NotFound` and is not created. Open still replays the log, which can write repaired pages, and WAL open can still truncate a torn tail.

## Cursors

A cursor holds its tree's read lock from the first successful seek until `Close`. On that keyspace, the same goroutine must not `Get`, `Put`, `Delete`, or `Commit` while the cursor is open.

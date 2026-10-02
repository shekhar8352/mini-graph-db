# ADR 0007: Disk engine, page-image redo, and recovery

- Status: accepted
- Date: 2026-10-02
- Phase: 2

## Context

Phases 2B–2D left a page file, a buffer pool, one B+tree per keyspace, and a binary WAL, unconnected. `storage.Engine` was implemented only in memory. Phase 2E has to acknowledge a commit only after it is durable, recover after a crash, checkpoint, and pass `storage/enginetest`.

The tree mutates pages in place. `Allocate`, `Free`, and root updates write the header immediately. The pool evicts a dirty frame by flushing it ([ADR 0004](0004-page-file.md)). A logical `TreeInsert` or `TreeDelete` cannot be replayed onto a tree whose split was only partly flushed. Format version stays 1. The shell still uses the memory engine and the text WAL.

## Decision

**Redo is a full page image per dirty page, bracketed by `TxnBegin` and `TxnCommit`.** The engine does not append `TreeInsert` or `TreeDelete`. Recovery ignores those types if an older file contains them. Uncommitted transactions never reach the heap file.

**Commit holds the engine lock through `wal.Sync`.** Readers do not observe the commit until the log is durable. The order is:

1. `BeforeCommit`. An error leaves the transaction open and writes nothing to the log.
2. `PageFile.Hold` stages header and page writes in memory. The heap file stays at the last publish.
3. Apply the write set to the live tree. Record an undo entry per key so other transactions keep the snapshot from their `Begin`.
4. Log `TxnBegin`, then data-page images in page-id order, then the header image, then `TxnCommit`. Each image's page LSN is the WAL record's LSN.
5. `wal.Sync`. On failure the staged bytes are discarded and the engine is marked broken. Later `Begin` fails. `Close` drops dirty frames and does not checkpoint.
6. Publish data pages, sync, write the header, sync, and invalidate the pool so the next read loads the published bytes.
7. Finish the transaction, run `AfterCommit`, and drop undo entries no open snapshot still needs.

Commits take one mutex, so two engine commits do not share a group-commit window. A checkpoint `Sync` can still overlap that window inside the WAL.

**Snapshots use an undo log, not a tree clone.** A reader at generation G uses the undo entry with the smallest generation greater than G. Its own writes override that. The B+tree stays the latest committed state.

**Open replays before it serves reads.** The page file is opened with the freelist check skipped. The WAL is opened, which truncates a torn tail. If the header's checkpoint LSN is past the end of the log, open returns `Corruption`. Committed page images are installed in log order; a transaction without `TxnCommit` is dropped. The freelist is checked after replay. A format version newer than 1 is refused before the checksum, by the page file and by the WAL.

**The checkpointer flushes, logs `Checkpoint`, syncs, stores `wal.Durable` in the header, syncs the heap, and deletes sealed segments whose end LSN is at or before that value.** The active segment stays. Remaining segment numbers stay contiguous and may start above 1. A hole is corruption. The background loop uses a 30s interval and 64 MiB of WAL since the last checkpoint. Either limit can be disabled. `Close` of a healthy read-write engine checkpoints. Read-only close does not. A broken engine abandons dirty state and does not checkpoint.

**Read-only open refuses a missing heap file and refuses read-write transactions.** Replay may still write repaired pages, and WAL open may still truncate a torn tail.

**A cursor holds its tree's read lock until `Close`.** `Get`, `Put`, `Delete`, and `Commit` on that keyspace from the same goroutine deadlock while the cursor is open.

The directory is `{dir}/db` for the heap and `{dir}/wal/*.wal` for the log. The constructor is `OpenEngine`.

## Alternatives considered

1. **Replay logical `TreeInsert` and `TreeDelete`.** A crash during a split would leave a tree those records cannot be applied to. Page images are idempotent: installing the same image twice is the same tree.
2. **Copy-on-write pages so a commit is a root swap.** That replaces the in-place tree and the dirty-eviction rule tests already depend on. Undo plus page images keeps both.
3. **Stop flushing dirty frames on eviction so the heap changes only at publish.** [ADR 0004](0004-page-file.md) tests require a dirty eviction to write the page. `Hold` stages those writes instead, and the file is unchanged until after `wal.Sync`.
4. **Acknowledge the commit before publishing pages, and let the caller proceed while the heap catches up.** The WAL already has the commit, which is what recovery needs. Doing the publish before returning keeps a clean `Close` from checkpointing over pages that exist only in the log.

## Consequences

- An acknowledged commit survives a process crash. Recovery reinstalls its page images if they are not already on the heap.
- The WAL stores whole pages, so a small update logs every page it dirtied, including the header.
- A crash during publish can leave the heap short of the header's page count. Open then fails before replay. Repairing a torn header from the WAL is not done here.
- A crash during allocate can leak a page. The freelist is not rebuilt. [ADR 0004](0004-page-file.md) prefers a leak over a double free.
- There is no relaxed durability mode. Every commit waits for `wal.Sync`.
- The shell and `internal/persist` are unchanged.

## Follow-up

- Phase 2F imports the legacy gob snapshot ([ADR 0008](0008-legacy-import.md)). The shell stays on the memory engine. `internal/persist` is gone.
- Phase 3 adds first-committer-wins. This engine still lets the last writer win.
- Phase 8 can copy sealed segments through `wal.Archiver`. Truncate must keep a segment the archiver has not finished.

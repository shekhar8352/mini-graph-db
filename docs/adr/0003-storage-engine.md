# ADR 0003: Storage engine interface and memory snapshots

- Status: accepted
- Date: 2026-09-25
- Phase: 2

## Context

Phase 2 replaces the gob snapshot with a crash-safe page store. That store does not exist yet. The first slice (2A) has to freeze the engine API, ship an in-memory implementation, and move graph operations onto it so later engines can be dropped in without a second graph implementation.

The roadmap leaves one layout choice open: one B+tree per keyspace, or a single B+tree with a 1-byte keyspace prefix. Phase 2C is the task that has to pick. The 2A interface must not decide it.

Constraints: `internal/storage` stays stdlib-only and must not import the query language. Graph semantics sit one layer up, in `internal/storage/graphstore`. Transactions in this phase are the engine's private write set. MVCC, timestamps, and first-committer-wins are Phase 3. The gob snapshot stays format version 1.

## Decision

**Keyspaces are an argument on the transaction, not bytes inside the key.** `Put`, `Get`, `Delete`, and `Cursor` take a `Keyspace`. The memory engine keeps one sorted key slice and one value map per keyspace. A disk engine can map each keyspace onto its own tree or onto a prefix in one tree without changing callers.

**Readers see a snapshot taken at Begin.** Commit clones the latest committed snapshot, applies the transaction's puts and tombstones, and publishes that clone. Other transactions do not see the write set before commit. A transaction sees its own writes. Two commits that touch the same key: the later commit wins. Detecting that conflict is Phase 3.

**Crash hooks are part of the interface.** `BeforeCommit` runs before the write set is visible; an error leaves the transaction open and the writes invisible. `AfterCommit` runs after the write set is visible; an error means the commit happened and the acknowledgement failed. `Crash` aborts every open transaction and keeps committed data. `Sync` is a no-op on the memory engine because there is no file.

**`internal/graph` is a facade.** Node and edge records, adjacency, the label index, the property index, cascade delete, and id counters live in graphstore and run on `storage.Tx`. The query executor, the shell, and the gob snapshot keep calling `graph.Graph`. Each of those calls auto-commits one transaction. IDs are allocated from the catalog keyspace and are not reused.

The memory structure is a sorted slice plus a map, not a skip list. Commits are serialized by one mutex. That is enough for the conformance suite and for the embedded shell.

## Alternatives considered

1. **A keyspace prefix byte inside every key.** That would bake the Phase 2C layout into the API. Callers would not be able to switch to one tree per keyspace without rewriting keys. The disk engine can still add a prefix later, inside its own encoder.
2. **A skip list.** A sorted slice and a map are simpler, use only the standard library, and are fast enough while the data set fits in memory. The structure that has to scale is the B+tree in Phase 2C.
3. **Read-committed gets.** A cursor could then observe a commit that lands in the middle of a scan. Snapshot-at-begin keeps one scan stable. It is also the read side Phase 3 will formalize.
4. **Leaving the old map-based graph in place beside graphstore.** The two implementations would drift. The facade is the only graph.

## Consequences

- `storage/enginetest` is the suite the disk engine must pass in Phase 2E.
- Lost updates on the same key are possible until Phase 3. Callers that need first-committer-wins have to wait.
- `Sync` does not make the memory engine durable. Acknowledged commits survive `Crash` only inside the process.
- The `V` keyspace is reserved and unused.

## Follow-up

- Phase 2C chose one B+tree per keyspace ([ADR 0005](0005-btree.md)).
- Phase 3 adds the timestamp oracle, version chains, and first-committer-wins on top of this write set.
- Phase 2F removes the gob snapshot after the importer exists. 2A does not.

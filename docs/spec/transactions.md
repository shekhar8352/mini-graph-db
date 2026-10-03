# Transactions

Snapshot isolation lives in `internal/txn`. The decision record is [ADR 0009](../adr/0009-mvcc.md). The page engine and the memory engine still apply last-writer-wins when they are used directly ([spec/engine.md](engine.md)).

`txn.Open` takes ownership of an engine and returns a `storage.Engine`. `Close` closes that engine. The shell opens the memory engine this way. `graph.New` does not: it stays on the raw memory engine.

## Timestamps

`Begin` assigns a transaction id and a snapshot equal to the latest commit timestamp. `Commit` of a transaction that wrote something assigns the next commit timestamp. A read at snapshot `S` sees a committed version when `createTS ≤ S` and the version has not been deleted at `S` (`deleteTS` is 0, or `deleteTS > S`). The transaction's own writes are visible to it before commit and invisible to everyone else.

The oracle is one record in keyspace `V`: format byte `1`, the next transaction id, and the last commit timestamp. Open refuses a newer format byte. A database written before the manager has no oracle; open wraps each raw value as a head with `createTS` 1 and commits that migration once.

## Versions

The current value of a key stays in that key's keyspace. The bytes are magic `0xA5`, `createTS`, `deleteTS`, and the payload. `deleteTS` 0 means the key is live. Older payloads are chain records in `V`, keyed by keyspace, user key, and `createTS`. User `Get`, `Put`, `Delete`, and `Cursor` reject keyspace `V`.

## Commit and abort

Writes stay in a private map until commit. Commit checks that no other commit created or deleted those keys after this snapshot. The loser gets `Conflict`, which is retryable, and the transaction stays open. `InsertUnique` that finds the key already live, or lost to a commit after the snapshot, gets `ConstraintViolation`, which is not retryable.

The versions and the oracle are written in one underlying storage transaction. On disk that transaction is the page-image commit: the log sync happens before the pages are published, and `TxnCommit` carries no write-set payload. A crash before that record leaves the transaction invisible. A crash after it shows every key in the write set.

`Rollback` drops the map. If the transaction had writes, the disk engine appends `TxnAbort` and syncs. A read-only or empty rollback does not. Recovery treats a transaction without `TxnCommit` as aborted either way.

An empty commit and a read-only commit do not write and do not run commit hooks.

## What snapshot isolation allows

Two transactions that read the same keys and write different keys both commit. That is write skew. `SELECT … FOR UPDATE` is not implemented.

A phantom insert of a key that `InsertUnique` claimed is rejected at commit.

## Statements

With no explicit transaction, each statement commits on its own. The legacy grammar also accepts:

```
BEGIN
BEGIN READ ONLY
COMMIT
ROLLBACK
VACUUM
```

`BEGIN READ` without `ONLY` is a syntax error. A second `BEGIN` fails. `COMMIT` and `ROLLBACK` without `BEGIN` fail. `BEGIN READ ONLY` rejects later writes. `VACUUM` removes versions no open snapshot can still read and prints how many it reclaimed.

`Graph` holds at most one explicit transaction. It is not safe for concurrent calls. Separate graphs can share one manager.

## Limits and vacuum

| Option | Zero means | Past the cap |
|--------|------------|--------------|
| `MaxOpenTxns` | 1024 | `ResourceExhausted` from `Begin` |
| `MaxWriteSet` | 100000 keys | `ResourceExhausted` from `Put` or `Delete` |
| `IdleTimeout` | disabled | `ResourceExhausted`, and the transaction is aborted |
| `GCInterval` | no background vacuum | — |

A negative `MaxOpenTxns` or `MaxWriteSet` disables that cap. `VACUUM` and `Manager.Vacuum` run the same collection. `MVCCStats.VersionsReclaimed` is `versions_reclaimed_total`. `MVCCStats.OldestSnapshotAge` is `oldest_snapshot_age_seconds`. Prometheus export is Phase 8.

## Locks

A transaction does not lock the keys it read or wrote. Commit takes one lock for validate and apply, then releases it. Two transactions cannot deadlock on each other's keys. A disk cursor still holds its tree lock until `Close`, which is separate from this rule.

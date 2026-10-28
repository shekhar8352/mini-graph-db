# ADR 0011: Blocking index build

- Status: accepted
- Date: 2026-10-28
- Phase: 5
- Deciders: implementer

## Context

Phase 5 adds declared secondary indexes. The roadmap allows either an online build (batches under a read snapshot, then catch-up of concurrent writes) or a blocking build behind a flag. Online build can leave `POPULATING` or `FAILED` rows in the catalog if the process crashes mid-build. The catalog lives in the `C` keyspace and is WAL-logged with the rest of the transaction.

Index and constraint DDL must survive a crash as fully present or fully absent (Phase 5 acceptance). Names are unique per kind. `SHOW INDEXES` already has a `state` column (`ONLINE`, `POPULATING`, `FAILED`).

## Decision

`CREATE INDEX` and `CREATE CONSTRAINT` run as one DDL transaction and populate the index before commit. The catalog row is written `ONLINE` in that same transaction. A crash or rollback leaves the catalog and the `P` keyspace unchanged for that name. `POPULATING` and `FAILED` exist on the type for `SHOW` and for a later online builder; a blocking build does not persist them.

Declared index keys in `P` use prefix `0xFF|indexID|EncodeComposite(values…)|entityID` so they do not collide with the implicit node property equality index (`propID|EncodeKey(v)|nodeID`). Null and non-storable values are not indexed.

## Alternatives considered

1. **Online build with catch-up** — readers keep writing during the build, and `SHOW` can report `POPULATING`. A crash can leave a half-built index that needs a sweeper or a `FAILED` row. Rejected for 1.0 because crash atomicity is an exit criterion and the extra catch-up protocol is larger than the feature.
2. **Blocking build behind a session flag, online by default** — two code paths and a flag that tests would have to set to get the atomic behavior. Rejected; one path is enough until online build has a follow-up ADR.

## Consequences

Large `CREATE INDEX` holds a write transaction until every matching node or edge is scanned. Concurrent writers wait or conflict. Drop of a missing name is `NotFound`; create of a duplicate name is `AlreadyExists`, matching the query-language spec.

## Follow-up

Online, batched index build with `POPULATING`/`FAILED` and catch-up of concurrent writes. Recorded in `docs/BACKLOG.md`.

# ADR 0012: gRPC server and multi-database layout

- Status: accepted
- Date: 2026-10-31
- Phase: 6
- Deciders: implementer

## Context

Phase 6 exposes the database over gRPC and runs more than one database from one data directory. The page format stays version 1. Database names are already checked by the semantic analyzer: `[a-z][a-z0-9_]{0,62}`, and `system` and `default` are reserved for `CREATE` and `DROP`. Authentication is Phase 7, so this server does not check callers.

The executor returns one `exec.Result` after a statement has finished. Streaming has to sit on top of that.

## Decision

Each database is a directory `data/<name>/` opened with `OpenEngine` and wrapped by `txn.Manager`. The `system` database holds the directory catalog in the catalog keyspace under kind `0x40` (version byte 1, then the create time). `default` is created on first start. User databases open on first use. `DROP DATABASE name CONFIRM` refuses `system` and `default`, refuses a database with an open transaction, and renames the directory to `data/.trash/<name>-<unixnano>`. A sweeper deletes trash older than `trash_retention`.

A gRPC connection has at most one server-side transaction. `txn_id` is the decimal form of a process-local int64. The id is not valid on another connection. Disconnect or idle timeout rolls it back. `Execute` streams columns, row batches, notifications, and a summary. A batch holds at most `max_rows_per_message` rows and stays under `max_message_bytes`.

`Value` is a protobuf `oneof` with one arm per `value.Kind`. Node and edge values are ids, matching `NodeRef` and `EdgeRef`.

Loopback may speak plaintext. Any other bind needs TLS unless `--insecure-plaintext` is set. `fsync = false` skips the WAL fsync and is logged at startup. The page format does not change.

## Alternatives considered

1. **One shared Graph per database** — `graph.Graph` holds a single explicit transaction and is not safe for concurrent use. Rejected. Each request gets its own `graph.Graph` over the shared engine, and an explicit transaction keeps that graph until commit or rollback.
2. **A side file for the database list** — easier to scan, but Phase 7 stores users and roles in `system`. Rejected. The catalog record lives in `system` so later auth data has one store.
3. **REST or a single-row protobuf message** — the product decision is gRPC only. A row batch is how `max_rows_per_message` is enforced without a second response type.

## Consequences

`CREATE DATABASE` and `DROP DATABASE` are server operations, not writes in the caller's graph transaction. The embedded shell still returns `Unavailable` for them. Kind `0x40` is additive and only written in `system`. A data directory laid out as a single engine (the `graphdb migrate --to` directory itself) is not adopted as a named database; the server expects `data/<name>/`.

The executor still materializes a statement before the server streams it, so a large result is buffered in the server even though the client receives it in batches.

## Follow-up

Phase 7 adds authentication on every non-health RPC. Phase 8 exports the in-process RPC counters to Prometheus. Pull-based execution, so a slow client bounds server memory, is recorded in the backlog.

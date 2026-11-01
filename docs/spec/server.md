# gRPC server

`graphdb server` listens for gRPC on `127.0.0.1:9530` unless `[server] bind` or `--bind` says otherwise. The contract is [`api/proto/graphdb/v1/graphdb.proto`](../../api/proto/graphdb/v1/graphdb.proto). Generated code is committed under `api/gen/graphdb/v1`. Regenerate it with `make proto`.

Authentication is not enforced. The process logs that at startup. Phase 7 adds it.

## Services

| Service | RPCs |
|---------|------|
| `graphdb.v1.QueryService` | `Execute`, `ExecuteBatch` |
| `graphdb.v1.TransactionService` | `Begin`, `Commit`, `Rollback`, `Keepalive` |
| `graphdb.v1.AdminService` | `CreateDatabase`, `DropDatabase`, `ListDatabases`, `ListTransactions`, `TerminateTransaction`, `Checkpoint`, `Vacuum`, `Stats` |
| `grpc.health.v1.Health` | standard health |
| reflection | `grpc.reflection.v1` |

`Execute` streams one statement at a time. Each statement sends columns (when it has any), notifications, row batches, and one summary. `statement_index` counts from zero across a script and across an `ExecuteBatch`. A row batch holds at most `max_rows_per_message` rows. A message larger than `max_message_bytes` fails with `ResourceExhausted`.

`txn_id` on a request must belong to the same connection. An empty `txn_id` uses that connection's open transaction when one exists, and auto-commits otherwise. The summary's `txn_id` is set while a transaction stays open. `Begin` returns the id. `SHOW TRANSACTIONS` returns `id`, `readOnly`, and `age`.

A failure is a gRPC status plus an `ErrorInfo` detail: the graphdb code (`Syntax`, `NotFound`, …), `retryable`, and `position` when the message contains `at line:column`.

| Graphdb code | gRPC code |
|--------------|-----------|
| `NotFound` | `NOT_FOUND` |
| `AlreadyExists` | `ALREADY_EXISTS` |
| `InvalidArgument`, `Syntax`, `Semantic` | `INVALID_ARGUMENT` |
| `ConstraintViolation` | `FAILED_PRECONDITION` |
| `Conflict` | `ABORTED` |
| `Unauthenticated` | `UNAUTHENTICATED` |
| `PermissionDenied` | `PERMISSION_DENIED` |
| `ResourceExhausted` | `RESOURCE_EXHAUSTED` |
| `Corruption` | `DATA_LOSS` |
| `Unavailable` | `UNAVAILABLE` |
| `Internal` | `INTERNAL` |

`Conflict`, `Unavailable`, and `ResourceExhausted` set `retryable`. A panic in a handler becomes `Internal`.

## Values

`Value` is a `oneof` with the same arms as `internal/value`: null, bool, int, float, string, bytes, date, datetime, duration, list, map, node, edge, and path. Node and edge messages carry only the id. Datetime stores UTC unix nanoseconds and, when `zone_set` is true, a fixed offset in seconds east of UTC.

## Databases

```
data/
  system/          catalog, including the database list
  default/         created on first start
  analytics/       opened on first use
  .trash/          dropped databases until trash_retention
  graphdb.pid
```

Each directory is an `OpenEngine` database wrapped by the transaction manager. The database list is stored in `system` under catalog kind `0x40`. Names match `[a-z][a-z0-9_]{0,62}`. `system` and `default` cannot be created or dropped.

`USE name` changes the connection's database. `ExecuteRequest.database` selects the database for that call when no transaction is open and does not, by itself, change the session. A transaction is tied to the database it started on.

`DROP DATABASE name CONFIRM` moves the directory to `data/.trash/`. The admin RPC sets `confirm: true` for the same effect. Dropping a database that has an open transaction fails.

## Configuration

Precedence is flags, then `GRAPHDB_*` environment variables, then `graphdb.toml`, then built-in defaults. `graphdb config print-defaults` writes a file. `graphdb config validate` loads the same overlay the server uses.

| Variable | Config |
|----------|--------|
| `GRAPHDB_SERVER_BIND` | `[server] bind` |
| `GRAPHDB_SERVER_MAX_CONNECTIONS` | `[server] max_connections` (default 1000) |
| `GRAPHDB_SERVER_SHUTDOWN_TIMEOUT` | `[server] shutdown_timeout` |
| `GRAPHDB_SERVER_PID_FILE` | `[server] pid_file` |
| `GRAPHDB_SERVER_INSECURE_PLAINTEXT` | `[server] insecure_plaintext` |
| `GRAPHDB_SERVER_TLS_CERT` | `[server.tls] cert` |
| `GRAPHDB_SERVER_TLS_KEY` | `[server.tls] key` |
| `GRAPHDB_SERVER_TLS_CLIENT_CA` | `[server.tls] client_ca` |
| `GRAPHDB_STORAGE_DATA_DIR` | `[storage] data_dir` |
| `GRAPHDB_STORAGE_PAGE_CACHE_BYTES` | `[storage] page_cache_bytes` |
| `GRAPHDB_STORAGE_CHECKPOINT_INTERVAL` | `[storage] checkpoint_interval` |
| `GRAPHDB_STORAGE_WAL_SEGMENT_BYTES` | `[storage] wal_segment_bytes` |
| `GRAPHDB_STORAGE_FSYNC` | `[storage] fsync` |
| `GRAPHDB_STORAGE_TRASH_RETENTION` | `[storage] trash_retention` |
| `GRAPHDB_LIMITS_QUERY_TIMEOUT` | `[limits] query_timeout` |
| `GRAPHDB_LIMITS_QUERY_MEMORY_BYTES` | `[limits] query_memory_bytes` |
| `GRAPHDB_LIMITS_MAX_TXN` | `[limits] max_txn` |
| `GRAPHDB_LIMITS_TXN_IDLE` | `[limits] txn_idle` |
| `GRAPHDB_LIMITS_MAX_ROWS_PER_MESSAGE` | `[limits] max_rows_per_message` |
| `GRAPHDB_LIMITS_MAX_MESSAGE_BYTES` | `[limits] max_message_bytes` |
| `GRAPHDB_LOG_LEVEL` | `[log] level` |
| `GRAPHDB_LOG_FORMAT` | `[log] format` |
| `GRAPHDB_METRICS_BIND` | `[metrics] bind` (recorded; nothing listens yet) |

`[auth]` is accepted and ignored until Phase 7.

A loopback bind may speak plaintext. Any other address needs `[server.tls]` or `--insecure-plaintext`. `graphdb admin gen-cert` writes a one-year self-signed certificate for localhost. Setting `client_ca` requires clients to present a certificate.

`fsync = false` skips the WAL fsync. The server logs that acknowledged commits can be lost on a crash. The default is `true`.

## Process

Startup writes `<data-dir>/graphdb.pid` unless `pid_file` is set. A live pid refuses to start. A dead pid is replaced. The process prints `listening <addr>` and logs the effective settings. Certificate paths are logged. File contents are not.

`SIGINT` or `SIGTERM` stops new calls, drains for `shutdown_timeout`, checkpoints, and closes. The next connection past `max_connections` receives `ResourceExhausted`.

```bash
graphdb admin gen-cert --cert dev.crt --key dev.key
graphdb server --data-dir ./data
grpcurl -plaintext 127.0.0.1:9530 list
```

# ADR 0008: Legacy gob import

- Status: accepted
- Date: 2026-10-02
- Phase: 2

## Context

Phase 2 replaces the gob snapshot and the text WAL with the disk engine. Existing files are format version 0 (one label, scalar properties) or version 1 (label set, interned property keys, `value.EncodeRecord`). The shell loaded those files on startup through `internal/persist`. Backup and restore of the disk engine are Phase 8.

The roadmap tells Phase 2F to import both files into a new database directory, then delete `internal/persist`. `SAVE` and `LOAD` stay in the grammar and tell the user to use `graphdb backup`.

## Decision

**`internal/compat/gobimport` is the only reader of the old files.** It decodes the gob snapshot, replays the text WAL as query lines on a memory graph, and writes the result with one `graphstore.Replace` into `OpenEngine`. Version 0 and version 1 load. A newer version is refused before the rest of the file is treated as data. A missing snapshot or WAL path is `NotFound`. A destination that already contains `db` is `AlreadyExists`.

**`graphdb migrate --from-legacy <snapshot> [--wal <file>] --to <data-dir>` is the command.** The shell does not open the disk directory and does not append a text log. `--db` and `--wal` are no longer shell flags. History still uses `--history`.

**`SAVE` and `LOAD` parse and then fail.** The error text is: use `graphdb backup`. They do not write a file. `internal/persist` is removed.

Replay runs in memory, then one commit publishes the whole graph. A crash before that commit leaves no database when the destination directory was created by the command. A crash after the commit is recovered by the disk engine.

## Alternatives considered

1. **Keep `internal/persist` as a read-only package.** The shell would still depend on it, and the roadmap says to remove it once the importer exists.
2. **Replay each WAL line as its own disk commit.** That fsyncs once per statement. One replace after replay is the same graph and one durable commit.
3. **Point the shell at `OpenEngine` in this phase.** That is the phase exit item for a `-engine=memory|disk` flag, not the migration task. The shell stays in memory.

## Consequences

- An old `graph.db` plus `graph.wal` can be moved into `{dir}/db` and `{dir}/wal`. Reopen uses the disk engine.
- The interactive shell forgets the graph when the process exits. Session history is the file that remains.
- `graphdb backup` is not implemented. The error string names the command Phase 8 will add.
- Gob type names from `internal/persist` still decode. The importer matches fields by name.

## Follow-up

- Phase 8 adds `graphdb backup` and restore for the disk directory.
- The phase 2 exit criteria still want a 20 GB reopen benchmark and REPL tests on both engines.

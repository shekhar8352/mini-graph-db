# Write-ahead log

This is the binary redo log written by `internal/wal`. Format version 1. The decision record is [ADR 0006](../adr/0006-wal.md). The disk engine applies it in [spec/engine.md](engine.md). The page file is [spec/pages.md](pages.md). The shell does not read this log. A legacy text WAL is replayed only by `graphdb migrate` ([spec/legacy.md](legacy.md)).

Integers are little-endian. Segment files live in one directory and are named `000000000001.wal`, `000000000002.wal`, and so on. Names that do not match that pattern are ignored. A new log starts at segment 1. `Truncate` deletes sealed segments whose end LSN is at or before the cutoff, so the oldest remaining number may be greater than 1. The numbers that remain do not skip. A hole is corruption. The active segment is kept.

## Segment header

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 4 | magic `GRWL` |
| 4 | 2 | format version |
| 6 | 2 | reserved, written 0, ignored on read |
| 8 | 8 | segment number, starting at 1 |
| 16 | 8 | LSN of the first record in this segment |
| 24 | 4 | CRC32C of bytes `[0, 24)` |

`Open` reads the version from the first 6 bytes and refuses a newer version before it checks the checksum. A file shorter than this header, or a header-sized file whose checksum does not match, is a torn create of the last segment: the file is removed and the log continues from the previous end. The same damage in an earlier segment is corruption.

## Record frame

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 8 | LSN |
| 8 | 8 | transaction id |
| 16 | 2 | record type |
| 18 | 2 | reserved, written 0, ignored on read |
| 20 | 4 | payload length |
| 24 | payload length | payload |
| 24 + payload length | 4 | CRC32C of the bytes before it |

The largest payload `Append` accepts is 16 MiB.

An LSN is the start of the record in the logical log. The first record is LSN 1. Each later LSN is the previous LSN plus the framed size of the previous record. Segment headers are not part of the LSN. LSN 0 means there is no record: a checkpoint that has not been written, or a page that has never been logged.

`Durable` is the first LSN that a successful `Sync` has not covered. Records with a smaller LSN are on disk. `Append` only buffers. `Sync` writes and fsyncs. `Close` syncs, then closes.

A record that runs past the end of the last segment is a torn tail. `Open` truncates the file to the start of that record. The reader stops there and does not return it. A record whose bytes are all present and whose checksum does not match is `Corruption`, even when it is the last record in the file.

## Record types

| Type | Value | Payload |
|------|------:|---------|
| PageWrite | 1 | `uint64` page id, then the page image |
| TreeInsert | 2 | `uint8` keyspace, `uint32` key length, key, `uint32` value length, value |
| TreeDelete | 3 | `uint8` keyspace, `uint32` key length, key |
| TxnBegin | 4 | empty |
| TxnCommit | 5 | empty in this version; the length prefix allows a later write set |
| TxnAbort | 6 | empty |
| Checkpoint | 7 | `int64` Unix nanoseconds |

A nil key or value is stored as empty. A page image must not be empty. An unknown type is `InvalidArgument` on append and `Corruption` if a segment already contains one.

## Segments and group commit

The default segment size is 64 MiB, including the header. `Append` does not split a record. If the next record would pass the cap and the segment already holds a record, that segment is synced and closed, and the record starts the next file. A record that is the first in a segment is written even when it is longer than the cap.

When more than one `Sync` is waiting, the leader pauses for the group-commit window (default 1 ms) and one fsync covers every record buffered by then. A `Sync` that is alone does not wait.

After a segment is synced and closed, `Archiver.Archive` is called with the file path, the segment number, and the LSN range `[First, End)`. The hook must be idempotent and must not call back into the log. If it returns an error, `Sync` fails and the next `Sync` calls the hook again. The active segment is not archived. `Open` does not call the hook.

## Errors

| Condition | Code |
|-----------|------|
| Empty directory path, segment size at or below the header, negative group-commit delay, unknown record type, payload over 16 MiB, empty page image, bad magic, newer or other unsupported format version | `InvalidArgument` |
| Checksum mismatch on a complete record or a sealed segment, LSN or segment-number discontinuity, truncated sealed segment, unknown type already on disk | `Corruption` |
| Short write, failed create, failed fsync | `Unavailable` |
| Use after `Close` | `wal: closed` |

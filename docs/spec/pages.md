# Page file

This is the on-disk heap written by `internal/storage/disk`. Format version 1. The B+tree and the WAL are not in this file yet; their page types are reserved below. The decision record is [ADR 0004](../adr/0004-page-file.md).

Integers are little-endian. Page ids are `uint64`. Page 0 is the file header and is never freed. A page id of zero is also the end of the freelist.

## File

`Create` picks the page size. `Open` refuses any other size when the caller passes one, and always refuses a format version other than 1. A version greater than 1 is reported as newer than this build. The check uses the first 36 bytes and does not require a valid checksum, so a later format can move the trailer and still be rejected.

| Constraint | Value |
|------------|-------|
| Magic | `GRDB` |
| Format version | 1 |
| Default page size | 8192 |
| Allowed page sizes | 4096, 8192, 16384, 32768, 65536 |
| Checksum | CRC32C (Castagnoli) of every byte before the trailer |

A file shorter than `pageCount * pageSize` is corruption. Bytes past that length are a torn extension and are ignored. The next allocation overwrites them.

## Page image

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 8 | page id |
| 8 | 8 | page LSN (0 until the WAL writes it) |
| 16 | 2 | page type |
| 18 | 2 | flags, written 0, ignored on read |
| 20 | 4 | reserved, written 0, ignored on read |
| 24 | pageSize − 28 | payload |
| pageSize − 4 | 4 | CRC32C of bytes `[0, pageSize-4)` |

| Type | Value | Written in this phase |
|------|------:|------------------------|
| Header | 1 | yes, page 0 only |
| Free | 2 | yes |
| Data | 3 | yes |
| Tree leaf | 4 | reserved |
| Tree internal | 5 | reserved |
| Overflow | 6 | reserved |

The page id stored in the header must match the page's position. A mismatch is corruption even when the checksum matches.

## File header

The header is the payload of page 0.

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 4 | magic `GRDB` |
| 4 | 2 | format version |
| 6 | 2 | reserved, 0 |
| 8 | 4 | page size |
| 12 | 16 | database id |
| 28 | 8 | creation time, Unix nanoseconds |
| 36 | 8 | last checkpoint LSN |
| 44 | 8 | freelist head, 0 if empty |
| 52 | 8 | page count, including page 0 |
| 60 | 8 | number of pages on the freelist |
| 68 | 72 | nine root page ids |

Root slots, in order: `N`, `E`, `O`, `I`, `L`, `T`, `P`, `C`, `V`. Zero means that tree has not been created. The slots do not decide whether Phase 2C uses one tree or one tree per keyspace.

A free page stores the next freelist id in the first eight bytes of its payload. The list is last-in, first-out.

## Crash order

These rules hold without a WAL. They prefer a leaked page to a page that is both live and free.

| Operation | Order |
|-----------|--------|
| Extend | write the new zeroed page, then publish `pageCount` |
| Reuse a free page | publish the new freelist head, then overwrite the page |
| Free | write the free page linked to the old head, then publish that page as the head |
| Truncate | publish the smaller header, then shorten the file |

Open walks the freelist. A cycle, a page past `pageCount`, a non-free page on the list, or a length that disagrees with the free count is corruption.

## Buffer pool

The pool caches data pages in a fixed number of frames. Page 0 is not cached. `Get` pins a page. The last `Unpin` makes it the most recently used unpinned frame. Eviction takes the least recently unpinned frame and writes it first when it is dirty. When every frame is pinned, the caller gets `ResourceExhausted`.

`FlushAll` and `Close` write every dirty frame and sync the file. The pool does not close the file. It is the only writer of a page it currently caches.

## Errors

| Condition | Code |
|-----------|------|
| Bad magic, empty path, bad page size, newer or older format, page-size mismatch, freeing a root or a free page, writing page 0 | `InvalidArgument` |
| Page id past the file | `NotFound` on read |
| Checksum mismatch, short page, page-id mismatch, broken freelist, file shorter than the header claims | `Corruption` |
| Short write or other write I/O error | `Unavailable` |
| No unpinned frame | `ResourceExhausted` |
| Use after `Close` | `disk: closed` |

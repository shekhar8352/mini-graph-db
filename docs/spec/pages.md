# Page file

This is the on-disk heap written by `internal/storage/disk`. Format version 1. The page file and buffer pool are [ADR 0004](../adr/0004-page-file.md). Leaf, internal, and overflow pages are the B+tree in [ADR 0005](../adr/0005-btree.md). The WAL is not in this file yet.

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

| Type | Value | Role |
|------|------:|------|
| Header | 1 | page 0 only |
| Free | 2 | freelist |
| Data | 3 | opaque payload |
| Tree leaf | 4 | B+tree leaf |
| Tree internal | 5 | B+tree internal page |
| Overflow | 6 | tail of a large value |

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

Root slots, in order: `N`, `E`, `O`, `I`, `L`, `T`, `P`, `C`, `V`. Each slot is the root of that keyspace's B+tree. Zero means the tree has no pages.

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

## B+tree

Each keyspace has its own tree, rooted at the matching header slot. Keys are ordered with `bytes.Compare`. There is no prefix compression. A separator is the first key of the right subtree: the leftmost key of that subtree's leaves. Search descends through the first separator that is strictly greater than the key, so an equal key goes to the right.

Leaf and internal payloads are slotted. Cell bytes grow upward from the header. A directory of uint16 cell offsets grows downward from the end of the payload. Slot `i` is the uint16 at `payloadSize - 2*(i+1)`.

### Leaf payload

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 2 | cell count |
| 2 | 2 | unused, written 0 |
| 4 | 8 | left sibling, 0 if none |
| 12 | 8 | right sibling, 0 if none |
| 20 | | cells, then the slot directory |

A leaf cell is:

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 2 | key length |
| 2 | 1 | flags, 0 inline, 1 overflow |
| 3 | 1 | reserved, written 0 |
| 4 | 4 | value length |
| 8 | key length | key |
| 8 + key length | value length, or 8 | inline value, or the first overflow page id |

A value longer than a quarter of the page uses the overflow flag. The cell then stores the full value length and an 8-byte page id instead of the bytes. A key must fit in an empty leaf beside that pointer. A longer key is `InvalidArgument`.

A leaf split copies the first key of the right leaf up as the separator and leaves that key in the leaf. Both sides are non-empty.

### Internal payload

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 2 | key count |
| 2 | 2 | reserved, written 0 |
| 4 | 8 | rightmost child |
| 12 | | cells, then the slot directory |

An internal cell is the separator and the child to its left:

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 2 | key length |
| 2 | 2 | pad, written 0 |
| 4 | 8 | left child |
| 12 | key length | separator |

`n` keys have `n+1` children. Child `i` for `i < n` is in cell `i`. Child `n` is the rightmost field in the header. An internal split promotes the middle separator and removes it from both sides.

A non-root page is underfull when the bytes it uses are below half the payload. Delete merges an underfull page into the left sibling when both fit, otherwise into the right sibling, otherwise it borrows one key. A sibling that lends a key keeps at least two. The root may stay underfull. An empty root leaf is freed and the slot returns to 0. An internal root with no keys is replaced by its only child.

### Overflow payload

| Offset | Size | Field |
|-------:|-----:|-------|
| 0 | 8 | next overflow page, 0 if this is the last |
| 8 | 4 | number of value bytes on this page |
| 12 | that length | value bytes |

The chain is written before the leaf cell that points at it is published. After a replace, the previous chain is freed. A crash can leak an overflow page. The value already reachable from the leaf stays.

## Buffer pool

The pool caches data pages in a fixed number of frames. Page 0 is not cached. `Get` pins a page. The last `Unpin` makes it the most recently used unpinned frame. Eviction takes the least recently unpinned frame and writes it first when it is dirty. When every frame is pinned, the caller gets `ResourceExhausted`.

`FlushAll` and `Close` write every dirty frame and sync the file. The pool does not close the file. It is the only writer of a page it currently caches.

## Errors

| Condition | Code |
|-----------|------|
| Bad magic, empty path, bad page size, newer or older format, page-size mismatch, freeing a root or a free page, writing page 0, a key or value that cannot fit | `InvalidArgument` |
| Page id past the file; a tree key that is not present | `NotFound` on read |
| Checksum mismatch, short page, page-id mismatch, broken freelist, file shorter than the header claims, a broken tree or overflow chain | `Corruption` |
| Short write or other write I/O error | `Unavailable` |
| No unpinned frame | `ResourceExhausted` |
| Use after `Close` | `disk: closed` |

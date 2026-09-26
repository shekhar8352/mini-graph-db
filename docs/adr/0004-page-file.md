# ADR 0004: Page file, freelist, and buffer pool

- Status: accepted
- Date: 2026-09-25
- Phase: 2

## Context

Phase 2B is the heap the B+tree and the WAL will sit on. The roadmap fixes the outline: 8 KiB pages, a `GRDB` header, CRC32C in a trailer, a freelist, and a buffer pool with pins and dirty tracking. It leaves the eviction policy, the freelist shape, and the exact bytes open. There is no WAL yet, so this phase cannot make torn writes disappear. It can choose an order that leaks space instead of aliasing a live page.

`internal/storage` stays stdlib-only. The page file does not implement `storage.Engine`. That assembly is Phase 2E. One tree per keyspace versus one tree with a prefix is still Phase 2C ([ADR 0003](0003-storage-engine.md)).

## Decision

**Format version 1.** The first 36 bytes of the file stay stable across versions: the 24-byte page header, the magic `GRDB`, the uint16 version, two reserved bytes, and the uint32 page size. Open reads that prefix and refuses a newer version before it checks the checksum, so a later checksum or payload can still be recognized as too new. Version 0 and any other older version are refused. There is no version 0 file in the wild.

**Every page is `pageSize` bytes.** The size is a power of two from 4096 to 65536, chosen at `Create` and stored in the header. The default is 8192. A 24-byte header holds the page id, the page LSN, and the page type. The last four bytes are CRC32C (Castagnoli) of everything before them. Flags and the reserved header word are written as zero; readers ignore them. Page types 4, 5, and 6 are the leaf, internal, and overflow layouts filled in by [ADR 0005](0005-btree.md). This phase writes only header, free, and data. Using those type codes does not bump the version. Changing field widths does.

**The freelist is an intrusive singly linked list.** The header stores the head. A free page stores the next id in the first eight payload bytes. Zero ends the list. Allocate pops the head. Free pushes. That is LIFO, needs no extra bitmap page, and is enough until a compaction pass exists.

**Metadata updates are ordered so a crash leaks a page instead of handing it out twice.** Extend writes the new page, then publishes the higher page count. Popping the freelist publishes the new head, then overwrites the page. Free writes the free page (already linked to the old head), then publishes the new head. A torn tail past the published page count is ignored on open. A file shorter than the published page count is corruption. `Truncate` publishes the smaller header before it shortens the file, and a retry of the same page count shortens the file again if the first shorten failed.

**The buffer pool is a fixed frame table with LRU eviction.** A frame leaves the list while it is pinned and returns at the most-recent end on the last unpin. Eviction takes the least-recently-unpinned frame, writing it first when it is dirty. If every frame is pinned, the pool returns `ResourceExhausted`. `FlushAll` and `Close` write the dirty list and sync. Hit, miss, and eviction hooks run without the pool lock and must not call back in. Page 0 is not cached. There is no background flusher here; the Phase 2E checkpointer calls `FlushAll`.

**Fault injection wraps `os.File`.** `storage/disk/fs.File` is the read, write, sync, and truncate surface. `Fault` can fail the next read, write, or sync, short-write, or queue writes until sync. Queued writes are visible to later reads. `Discard` drops the queue. That is how tests build a torn page without a custom filesystem.

## Alternatives considered

1. **CLOCK eviction.** It touches less on a hit. The pool is a small table of frames, and LRU makes the eviction tests obvious. CLOCK can replace it later without a format change.
2. **A freelist bitmap or a page of free ids.** A bitmap finds double-frees without reading the page, and a packed list frees many pages in one write. Both need their own page layout. The intrusive list reuses the free page itself, and double-free is caught by the page type.
3. **Putting the checksum in the header.** A torn write could then look valid if the header was written and the tail was not, or the reverse. The trailer covers the bytes before it, so a torn page fails the check.
4. **Refusing to open a file with any extra trailing bytes.** Growth writes the new page before the header. Those extra bytes are the crash case this order produces. Treating them as fatal would turn a leak into an unopenable file.

## Consequences

- Phase 2C uses page types 4–6 and the root slots without a version bump. Each slot is one tree ([ADR 0005](0005-btree.md)).
- A crash in this phase can leak a page or leave a torn tail. It must not make a live page look free. The WAL in Phase 2E is what makes a committed write durable across that crash. `Sync` only flushes what has already been written.
- Callers that mix `PageFile.WritePage` with a pool that has the same page cached will diverge. The pool is the writer for pages it holds.
- Checksum failures, short reads, page-id mismatches, and a freelist that does not match the header are `Corruption`. A newer format version is `InvalidArgument` with an explicit message.

## Follow-up

- Phase 2C laid out leaf, internal, and overflow pages and chose one tree per keyspace ([ADR 0005](0005-btree.md)).
- Phase 2D frames WAL records with the same CRC32C.
- Phase 2E orders WAL durability ahead of these page writes and runs the checkpointer.

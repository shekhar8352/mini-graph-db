# ADR 0005: B+tree shape, delete, and overflow

- Status: accepted
- Date: 2026-09-26
- Phase: 2

## Context

Phase 2C stores ordered keys on the page file from [ADR 0004](0004-page-file.md). The header already has one root slot per keyspace (`N E O I L T P C V`). [ADR 0003](0003-storage-engine.md) made the keyspace an argument of the storage API and left the on-disk choice open: one B+tree per keyspace, or one tree whose keys start with a keyspace byte.

The roadmap also leaves prefix compression optional, and it allows either a real delete (borrow or merge) or a lazy delete that waits for compaction. There is no compaction pass yet. Values can be larger than a page (property maps, long strings, bytes). Format version 1 already reserved page types 4, 5, and 6 for a leaf, an internal page, and an overflow page. The WAL is Phase 2D, and the disk engine that ties the tree to `storage.Engine` is Phase 2E. This phase does not implement either.

## Decision

**One B+tree per keyspace.** `OpenTree` binds a tree to the header slot for that keyspace. A zero slot is an empty tree. The first `Put` allocates the root. Keys are the caller's bytes, ordered with `bytes.Compare`. An empty key is a real key. A nil value is stored as an empty value. `Get` of a missing key is `storage.ErrNotFound`. `Delete` of a missing key succeeds.

**No prefix compression.** Each cell stores its key in full. Cells stay independent, so a split, a borrow, and a decode do not need the previous key.

**Slotted pages.** Cell bytes grow up from a small header. A directory of uint16 offsets grows down from the end of the payload. A leaf cell holds the key and either the value or, when the value is longer than a quarter of the page, the value length and the id of the first overflow page. An internal cell holds a separator and the child to its left. The rightmost child sits in the internal header. A separator is the first key of the right subtree, copied from the leftmost leaf of that subtree. Search takes the first separator strictly greater than the key, so an equal key goes right. Leaf splits copy that separator and leave it in the right leaf. Internal splits promote the middle separator and drop it from both sides. The byte layout is in [spec/pages.md](../spec/pages.md). Format version stays 1.

**Delete borrows or merges.** A page is underfull when its used bytes are below half the payload. The root may be underfull. After a delete, an underfull non-root page merges into its left sibling when the combined page fits, otherwise into its right sibling, otherwise it borrows one key. A sibling keeps at least two keys. An empty root leaf is freed and the slot returns to zero. An internal root with no keys is replaced by its only child. Overflow chains are written before the leaf cell is published. The previous chain is freed after a successful replace. The same crash order as the freelist applies: a crash can leak a page. It must not drop the value that was already reachable.

**Cursors and locking.** `Seek` is the first key greater than or equal to the target. `SeekReverse` is the last key less than or equal to the target. A nil target is the first key, or the last key for `SeekReverse`. The tree uses a reader-writer lock. Readers share it. A writer is exclusive. A cursor holds the read lock until `Close`, so the same goroutine must not `Get`, `Put`, or `Delete` that tree while the cursor is open.

## Alternatives considered

1. **One tree and a one-byte keyspace prefix.** It is a single root and a simpler freelist of trees. It also puts the keyspace inside the key, which [ADR 0003](0003-storage-engine.md) refused at the API, and it makes a scan of one keyspace walk a shared leaf chain. The header already has a slot per keyspace. Using those slots keeps a scan inside one tree.
2. **Prefix compression.** Shared prefixes would shrink index pages. They also make a cell depend on its neighbor, so a torn page or a borrow has to rebuild the prefix. Full keys are simpler, and the cells are already variable length. A later format can add compression without changing the separator rule.
3. **Lazy delete plus compaction.** A tombstone avoids borrow and merge. Without a compaction pass, scans and page occupancy would keep the dead keys. Borrow and merge keep every live key reachable and reclaim space on the delete that created the hole.

## Consequences

- Phase 2E opens one tree per keyspace on the existing slots. It does not encode a prefix.
- A multi-page split is not atomic. Durability of a committed tree update waits on the WAL and the checkpointer in Phases 2D and 2E. Until then the shell stays on the memory engine.
- A key that cannot sit in an empty leaf next to an overflow pointer is `InvalidArgument`. Values longer than a quarter of the page use one or more overflow pages.
- Structural checks used by tests require strict key order, a separator equal to the leftmost leaf key of the right child, matching sibling links, and one leaf depth.

## Follow-up

- Phase 2D frames WAL records with the same CRC32C.
- Phase 2E implements `storage/disk.Engine` on this tree and orders WAL durability ahead of page writes.
- Prefix compression, if it is ever worth the complexity, is a later format change.

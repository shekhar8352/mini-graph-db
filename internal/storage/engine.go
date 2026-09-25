// Package storage is the key-value engine interface shared by the in-memory
// engine and, later, the page-based engine. Graph semantics live in graphstore.
package storage

import "errors"

// Keyspace is one ordered key range. The memory engine stores each keyspace
// in its own map. Whether the disk engine uses one B+tree or one tree per
// keyspace is left to Phase 2C; callers never encode that choice into keys.
type Keyspace byte

// Keyspace identifiers match the roadmap names. KSVersion is reserved for
// Phase 3 and is not written by this phase.
const (
	// KSNode stores node records keyed by node id.
	KSNode Keyspace = 'N'
	// KSEdge stores edge records keyed by edge id.
	KSEdge Keyspace = 'E'
	// KSOut stores outgoing adjacency keys.
	KSOut Keyspace = 'O'
	// KSIn stores incoming adjacency keys.
	KSIn Keyspace = 'I'
	// KSLabel stores the label index.
	KSLabel Keyspace = 'L'
	// KSEdgeType stores the edge-type index.
	KSEdgeType Keyspace = 'T'
	// KSProp stores the property index.
	KSProp Keyspace = 'P'
	// KSCatalog stores names, ids, and counters.
	KSCatalog Keyspace = 'C'
	// KSVersion is reserved for MVCC version chains.
	KSVersion Keyspace = 'V'
)

// Sentinel errors returned by every engine. Callers should use errors.Is.
var (
	// ErrNotFound is returned when a key is absent.
	ErrNotFound = errors.New("storage: not found")
	// ErrReadOnly is returned by Put and Delete on a read-only transaction.
	ErrReadOnly = errors.New("storage: read-only transaction")
	// ErrClosed is returned when the engine has been closed.
	ErrClosed = errors.New("storage: closed")
	// ErrDone is returned when a transaction was already committed or rolled back.
	ErrDone = errors.New("storage: transaction finished")
)

// TxOptions selects a read-only or read-write transaction.
type TxOptions struct {
	// ReadOnly forbids Put and Delete. Commit of a read-only transaction
	// publishes nothing.
	ReadOnly bool
}

// Hooks are crash-simulation points. They run on the committing transaction.
// Hooks must not call back into the engine.
//
// BeforeCommit runs before the write set becomes visible. A non-nil error
// leaves the transaction open and the writes invisible.
// AfterCommit runs after the write set is visible. A non-nil error means the
// commit is published and the acknowledgement failed.
type Hooks struct {
	BeforeCommit func() error
	AfterCommit  func() error
}

// Stats is a snapshot of committed engine state. Uncommitted writes are omitted.
type Stats struct {
	Keys      int
	Commits   uint64
	Rollbacks uint64
}

// Engine is a key-value store split into keyspaces.
// A transaction is not safe for concurrent use. The engine is: many
// transactions may be open, and commits are serialized.
type Engine interface {
	// Begin starts a transaction. A read-write transaction sees the committed
	// snapshot from Begin plus its own writes. Other transactions do not see
	// those writes until Commit.
	Begin(opts TxOptions) (Tx, error)
	// Sync is the durability barrier. The memory engine has nothing to flush.
	Sync() error
	// Stats reports committed keys and commit counters.
	Stats() Stats
	// SetHooks installs crash-simulation hooks, replacing any previous hooks.
	SetHooks(Hooks)
	// Crash aborts every open transaction. Committed data is kept and the
	// engine stays open.
	Crash() error
	// Close aborts open transactions and rejects new ones. Close is idempotent.
	Close() error
}

// Tx is a single transaction. Keys and values passed to Put are copied.
// Get, Key, and Value return copies.
type Tx interface {
	// Get returns a copy of the value, or ErrNotFound.
	Get(ks Keyspace, key []byte) ([]byte, error)
	// Put records a write. A nil value stores an empty present value.
	Put(ks Keyspace, key, val []byte) error
	// Delete records a tombstone. Deleting a missing key is a no-op success.
	Delete(ks Keyspace, key []byte) error
	// Cursor returns an unpositioned cursor over ks. The cursor sees this
	// transaction's view as of each Seek.
	Cursor(ks Keyspace) (Cursor, error)
	// Commit publishes the write set. A read-only commit publishes nothing.
	Commit() error
	// Rollback drops the write set.
	Rollback() error
}

// Cursor walks one keyspace in byte order.
// Seek(nil) lands on the first key. Seek of an empty slice lands on the first
// key greater than or equal to the empty key. SeekReverse(nil) lands on the
// last key. The cursor is invalid until a seek succeeds, and after Next or
// Prev walks off the end.
type Cursor interface {
	Seek(target []byte) bool
	SeekReverse(target []byte) bool
	Next() bool
	Prev() bool
	Key() []byte
	Value() []byte
	Valid() bool
	Close() error
}

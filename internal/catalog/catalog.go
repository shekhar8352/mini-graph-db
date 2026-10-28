// Package catalog stores declared schema objects in the catalog keyspace.
// Labels, edge types, property keys, and id counters stay interned in
// graphstore. This package owns indexes, constraints, ANALYZE stats, and
// the catalog format version. It uses storage.Tx only.
package catalog

import (
	"encoding/binary"
	"errors"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

// FormatVersion is the catalog record version written on first DDL.
const FormatVersion uint32 = 1

// HistBuckets is the number of equi-depth histogram buckets ANALYZE stores.
const HistBuckets = 64

// AnalyzeAfter is the write count that triggers an automatic ANALYZE.
const AnalyzeAfter uint64 = 10000

// Catalog record kinds. 0x00–0x1F and 0x81–0x83 are graphstore intern
// records and UNIQUE index keys. These kinds must not overlap them.
const (
	KindIndex      byte = 0x20
	KindConstraint byte = 0x21
	KindStats      byte = 0x22
	KindFormat     byte = 0x23
	KindNextIndex  byte = 0x24
	KindWriteCount byte = 0x25
	KindGeneration byte = 0x26
	KindUniqueLock byte = 0x27
)

// Entity is node or edge.
type Entity uint8

// Entity kinds.
const (
	NodeEntity Entity = 1
	EdgeEntity Entity = 2
)

func (e Entity) String() string {
	if e == EdgeEntity {
		return "edge"
	}
	return "node"
}

// State is the SHOW INDEXES / SHOW CONSTRAINTS state.
type State uint8

// Index and constraint states.
const (
	Online     State = 1
	Populating State = 2
	Failed     State = 3
)

func (s State) String() string {
	switch s {
	case Populating:
		return "POPULATING"
	case Failed:
		return "FAILED"
	default:
		return "ONLINE"
	}
}

// Index is one declared secondary index.
type Index struct {
	ID     uint32
	Name   string
	Entity Entity
	Label  string
	Props  []string
	Unique bool
	State  State
}

// ConstraintKind is UNIQUE, EXISTS, or TYPE.
type ConstraintKind uint8

// Constraint kinds.
const (
	UniqueConstraint ConstraintKind = 1
	ExistsConstraint ConstraintKind = 2
	TypeConstraint   ConstraintKind = 3
)

func (k ConstraintKind) String() string {
	switch k {
	case ExistsConstraint:
		return "exists"
	case TypeConstraint:
		return "type"
	default:
		return "unique"
	}
}

// Constraint is one declared constraint.
type Constraint struct {
	Name      string
	Kind      ConstraintKind
	Entity    Entity
	Label     string
	Prop      string
	TypeName  string
	IndexName string
}

// Bucket is one equi-depth histogram bucket. Upper is an EncodeKey.
type Bucket struct {
	Upper []byte
	Count int64
	NDV   int64
}

// Histogram is an equi-depth distribution of one property.
type Histogram struct {
	Count   int64
	NDV     int64
	Nulls   int64
	Min     []byte
	Max     []byte
	Buckets []Bucket
}

// Stats is the ANALYZE snapshot the planner reads.
type Stats struct {
	Nodes     int64
	Edges     int64
	Labels    map[string]int64
	EdgeTypes map[string]int64
	Props     map[string]Histogram
}

// Schema is one catalog snapshot.
type Schema struct {
	Format      uint32
	Generation  uint64
	Indexes     []Index
	Constraints []Constraint
	Stats       Stats
}

// IndexByName returns a declared index.
func (s *Schema) IndexByName(name string) (Index, bool) {
	if s == nil {
		return Index{}, false
	}
	for _, ix := range s.Indexes {
		if ix.Name == name {
			return ix, true
		}
	}
	return Index{}, false
}

// ConstraintByName returns a declared constraint.
func (s *Schema) ConstraintByName(name string) (Constraint, bool) {
	if s == nil {
		return Constraint{}, false
	}
	for _, c := range s.Constraints {
		if c.Name == name {
			return c, true
		}
	}
	return Constraint{}, false
}

// OnlineIndexes returns ONLINE indexes for entity and label or type.
func (s *Schema) OnlineIndexes(entity Entity, label string) []Index {
	if s == nil {
		return nil
	}
	var out []Index
	for _, ix := range s.Indexes {
		if ix.State != Online || ix.Entity != entity {
			continue
		}
		if ix.Label != "" && ix.Label != label {
			continue
		}
		out = append(out, ix)
	}
	return out
}

// PropKey names a histogram as n:Label.prop or e:Type.prop.
func PropKey(node bool, label, prop string) string {
	if node {
		return "n:" + label + "." + prop
	}
	return "e:" + label + "." + prop
}

// Load reads the catalog. A missing format is version 1 with generation 0.
func Load(tx storage.Tx) (*Schema, error) {
	format, err := readU32(tx, KindFormat, "")
	if err != nil {
		return nil, err
	}
	if format == 0 {
		format = FormatVersion
	}
	if format > FormatVersion {
		return nil, errors.New("catalog format is newer than this binary")
	}
	gen, err := readU64(tx, KindGeneration, "")
	if err != nil {
		return nil, err
	}
	indexes, err := listIndexes(tx)
	if err != nil {
		return nil, err
	}
	cons, err := listConstraints(tx)
	if err != nil {
		return nil, err
	}
	st, err := loadStats(tx)
	if err != nil {
		return nil, err
	}
	return &Schema{Format: format, Generation: gen, Indexes: indexes, Constraints: cons, Stats: st}, nil
}

// Generation returns the catalog generation for cache invalidation.
func Generation(tx storage.Tx) (uint64, error) {
	return readU64(tx, KindGeneration, "")
}

// PutIndex stores an index definition.
func PutIndex(tx storage.Tx, ix Index) error {
	if ix.Name == "" || len(ix.Props) == 0 {
		return errors.New("index needs a name and a property")
	}
	if ix.State == 0 {
		ix.State = Online
	}
	if ix.Entity == 0 {
		ix.Entity = NodeEntity
	}
	if err := ensureFormat(tx); err != nil {
		return err
	}
	if err := tx.Put(storage.KSCatalog, key(KindIndex, ix.Name), encodeIndex(ix)); err != nil {
		return err
	}
	return bump(tx)
}

// DropIndex removes an index definition. It reports whether the name existed.
func DropIndex(tx storage.Tx, name string) (bool, error) {
	k := key(KindIndex, name)
	_, err := tx.Get(storage.KSCatalog, k)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := tx.Delete(storage.KSCatalog, k); err != nil {
		return false, err
	}
	return true, bump(tx)
}

// PutConstraint stores a constraint definition.
func PutConstraint(tx storage.Tx, c Constraint) error {
	if c.Name == "" || c.Prop == "" {
		return errors.New("constraint needs a name and a property")
	}
	if c.Entity == 0 {
		c.Entity = NodeEntity
	}
	if c.Kind == 0 {
		c.Kind = UniqueConstraint
	}
	if err := ensureFormat(tx); err != nil {
		return err
	}
	if err := tx.Put(storage.KSCatalog, key(KindConstraint, c.Name), encodeConstraint(c)); err != nil {
		return err
	}
	return bump(tx)
}

// DropConstraint removes a constraint definition. It reports whether the name existed.
func DropConstraint(tx storage.Tx, name string) (bool, error) {
	k := key(KindConstraint, name)
	_, err := tx.Get(storage.KSCatalog, k)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := tx.Delete(storage.KSCatalog, k); err != nil {
		return false, err
	}
	return true, bump(tx)
}

// AllocIndexID returns the next index id, starting at 1.
func AllocIndexID(tx storage.Tx) (uint32, error) {
	next, err := readU64(tx, KindNextIndex, "")
	if err != nil {
		return 0, err
	}
	if next == 0 {
		next = 1
	}
	if err := writeU64(tx, KindNextIndex, "", next+1); err != nil {
		return 0, err
	}
	if err := bump(tx); err != nil {
		return 0, err
	}
	return uint32(next), nil
}

// PutStats stores ANALYZE output and clears the write counter.
func PutStats(tx storage.Tx, st Stats) error {
	if err := ensureFormat(tx); err != nil {
		return err
	}
	if err := tx.Put(storage.KSCatalog, key(KindStats, ""), encodeStats(st)); err != nil {
		return err
	}
	if err := writeU64(tx, KindWriteCount, "", 0); err != nil {
		return err
	}
	return bump(tx)
}

// AddWrites increments the mutation counter used for auto-ANALYZE.
func AddWrites(tx storage.Tx, n uint64) error {
	if n == 0 {
		return nil
	}
	cur, err := readU64(tx, KindWriteCount, "")
	if err != nil {
		return err
	}
	return writeU64(tx, KindWriteCount, "", cur+n)
}

// WriteCount returns the mutations since the last ANALYZE.
func WriteCount(tx storage.Tx) (uint64, error) {
	return readU64(tx, KindWriteCount, "")
}

func listIndexes(tx storage.Tx) ([]Index, error) {
	keys, err := scanKind(tx, KindIndex)
	if err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(keys))
	for _, k := range keys {
		name, ok := parseName(k, KindIndex)
		if !ok {
			continue
		}
		raw, err := tx.Get(storage.KSCatalog, k)
		if err != nil {
			return nil, err
		}
		ix, err := decodeIndex(name, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, ix)
	}
	sortIndexes(out)
	return out, nil
}

func listConstraints(tx storage.Tx) ([]Constraint, error) {
	keys, err := scanKind(tx, KindConstraint)
	if err != nil {
		return nil, err
	}
	out := make([]Constraint, 0, len(keys))
	for _, k := range keys {
		name, ok := parseName(k, KindConstraint)
		if !ok {
			continue
		}
		raw, err := tx.Get(storage.KSCatalog, k)
		if err != nil {
			return nil, err
		}
		c, err := decodeConstraint(name, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sortConstraints(out)
	return out, nil
}

func loadStats(tx storage.Tx) (Stats, error) {
	raw, err := tx.Get(storage.KSCatalog, key(KindStats, ""))
	if errors.Is(err, storage.ErrNotFound) {
		return Stats{Labels: map[string]int64{}, EdgeTypes: map[string]int64{}, Props: map[string]Histogram{}}, nil
	}
	if err != nil {
		return Stats{}, err
	}
	return decodeStats(raw)
}

func ensureFormat(tx storage.Tx) error {
	cur, err := readU32(tx, KindFormat, "")
	if err != nil {
		return err
	}
	if cur == 0 {
		return writeU32(tx, KindFormat, "", FormatVersion)
	}
	if cur > FormatVersion {
		return errors.New("catalog format is newer than this binary")
	}
	return nil
}

func bump(tx storage.Tx) error {
	n, err := readU64(tx, KindGeneration, "")
	if err != nil {
		return err
	}
	return writeU64(tx, KindGeneration, "", n+1)
}

func key(kind byte, name string) []byte {
	buf := []byte{kind}
	buf = binary.AppendUvarint(buf, uint64(len(name)))
	return append(buf, name...)
}

func parseName(k []byte, kind byte) (string, bool) {
	if len(k) == 0 || k[0] != kind {
		return "", false
	}
	n, r := binary.Uvarint(k[1:])
	if r <= 0 {
		return "", false
	}
	rest := k[1+r:]
	if uint64(len(rest)) != n {
		return "", false
	}
	return string(rest), true
}

func readU64(tx storage.Tx, kind byte, name string) (uint64, error) {
	raw, err := tx.Get(storage.KSCatalog, key(kind, name))
	if errors.Is(err, storage.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(raw) != 8 {
		return 0, errors.New("catalog counter")
	}
	return binary.BigEndian.Uint64(raw), nil
}

func writeU64(tx storage.Tx, kind byte, name string, n uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	return tx.Put(storage.KSCatalog, key(kind, name), buf[:])
}

func readU32(tx storage.Tx, kind byte, name string) (uint32, error) {
	raw, err := tx.Get(storage.KSCatalog, key(kind, name))
	if errors.Is(err, storage.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(raw) != 4 {
		return 0, errors.New("catalog u32")
	}
	return binary.BigEndian.Uint32(raw), nil
}

func writeU32(tx storage.Tx, kind byte, name string, n uint32) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], n)
	return tx.Put(storage.KSCatalog, key(kind, name), buf[:])
}

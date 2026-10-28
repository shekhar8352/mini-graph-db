// Package index encodes and looks up declared secondary indexes in KSProp.
// Keys are 0xFF|indexID|EncodeComposite(values…)|entityID. Null and
// non-storable values are not indexed. The implicit node property index
// (propID|EncodeKey|nodeID) is unchanged.
package index

import (
	"bytes"
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Marker is the first byte of a declared index key. Implicit property keys
// start with a property id whose high byte is 0 until billions of names exist.
const Marker byte = 0xFF

// Encode is one index entry. vals must not contain null.
func Encode(id uint32, vals []value.Value, entity uint64) []byte {
	p := Prefix(id, vals)
	buf := make([]byte, len(p)+8)
	copy(buf, p)
	binary.BigEndian.PutUint64(buf[len(p):], entity)
	return buf
}

// Prefix is the key without the entity id, used for equality and range bounds.
func Prefix(id uint32, vals []value.Value) []byte {
	p := IDPrefix(id)
	if len(vals) == 0 {
		return p
	}
	return append(p, value.EncodeComposite(vals...)...)
}

// IDPrefix is every key of one index.
func IDPrefix(id uint32) []byte {
	buf := make([]byte, 5)
	buf[0] = Marker
	binary.BigEndian.PutUint32(buf[1:], id)
	return buf
}

// StartsPrefix is a STARTS WITH bound: the string key encoding without its
// terminator, so every string that begins with s sorts after this prefix.
func StartsPrefix(id uint32, s string) []byte {
	enc := value.EncodeKey(value.String(s))
	if len(enc) > 0 && enc[len(enc)-1] == 0x00 {
		enc = enc[:len(enc)-1]
	}
	return append(IDPrefix(id), enc...)
}

// Parse splits a declared index key. ok is false for implicit property keys.
func Parse(k []byte) (id uint32, vals []value.Value, entity uint64, ok bool) {
	if len(k) < 13 || k[0] != Marker {
		return 0, nil, 0, false
	}
	id = binary.BigEndian.Uint32(k[1:5])
	entity = binary.BigEndian.Uint64(k[len(k)-8:])
	mid := k[5 : len(k)-8]
	if len(mid) == 0 {
		return id, nil, entity, true
	}
	vals, err := value.DecodeComposite(mid)
	if err != nil {
		return 0, nil, 0, false
	}
	return id, vals, entity, true
}

// Indexable reports whether every value can be stored in an index entry.
func Indexable(vals []value.Value) bool {
	if len(vals) == 0 {
		return false
	}
	for _, v := range vals {
		if v.Kind() == value.KindNull || !v.Storable() {
			return false
		}
	}
	return true
}

// Put writes one entry. Null values are skipped.
func Put(tx storage.Tx, id uint32, vals []value.Value, entity uint64) error {
	if !Indexable(vals) {
		return nil
	}
	return tx.Put(storage.KSProp, Encode(id, vals, entity), nil)
}

// Delete removes one entry. A missing key is ignored.
func Delete(tx storage.Tx, id uint32, vals []value.Value, entity uint64) error {
	if !Indexable(vals) {
		return nil
	}
	return tx.Delete(storage.KSProp, Encode(id, vals, entity))
}

// Equal returns entity ids with exactly vals, in key order.
func Equal(tx storage.Tx, id uint32, vals []value.Value) ([]uint64, error) {
	if !Indexable(vals) {
		return nil, nil
	}
	return collectPrefix(tx, Prefix(id, vals))
}

// PrefixScan returns entity ids whose leading properties equal vals.
func PrefixScan(tx storage.Tx, id uint32, vals []value.Value) ([]uint64, error) {
	if len(vals) == 0 {
		return collectPrefix(tx, IDPrefix(id))
	}
	if !Indexable(vals) {
		return nil, nil
	}
	return collectPrefix(tx, Prefix(id, vals))
}

// Range returns entity ids with composite keys in [lo, hi] according to the
// inclusive flags. A nil bound is unbounded on that side.
func Range(tx storage.Tx, id uint32, lo, hi []value.Value, loInc, hiInc bool) ([]uint64, error) {
	idPre := IDPrefix(id)
	var start []byte
	var loComp, hiComp []byte
	if len(lo) > 0 {
		if !Indexable(lo) {
			return nil, nil
		}
		start = Prefix(id, lo)
		loComp = start[5:]
	} else {
		start = idPre
	}
	if len(hi) > 0 {
		if !Indexable(hi) {
			return nil, nil
		}
		hiComp = Prefix(id, hi)[5:]
	}
	keys, err := walk(tx, start, idPre)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, k := range keys {
		comp := k[5 : len(k)-8]
		if loComp != nil {
			cmp := comparePrefix(comp, loComp)
			if cmp < 0 || cmp == 0 && !loInc {
				continue
			}
		}
		if hiComp != nil {
			cmp := comparePrefix(comp, hiComp)
			if cmp > 0 || cmp == 0 && !hiInc {
				continue
			}
		}
		ids = append(ids, binary.BigEndian.Uint64(k[len(k)-8:]))
	}
	return ids, nil
}

func comparePrefix(comp, bound []byte) int {
	if len(comp) >= len(bound) {
		return bytes.Compare(comp[:len(bound)], bound)
	}
	return bytes.Compare(comp, bound)
}

// StartsWith returns entity ids whose leftmost string property starts with s.
func StartsWith(tx storage.Tx, id uint32, s string) ([]uint64, error) {
	return collectPrefix(tx, StartsPrefix(id, s))
}

// Drop removes every key of one index.
func Drop(tx storage.Tx, id uint32) error {
	keys, err := walk(tx, IDPrefix(id), IDPrefix(id))
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := tx.Delete(storage.KSProp, k); err != nil {
			return err
		}
	}
	return nil
}

func collectPrefix(tx storage.Tx, prefix []byte) ([]uint64, error) {
	keys, err := walk(tx, prefix, prefix)
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(keys))
	for _, k := range keys {
		if !bytes.HasPrefix(k, prefix) || len(k) < 13 {
			continue
		}
		ids = append(ids, binary.BigEndian.Uint64(k[len(k)-8:]))
	}
	return ids, nil
}

// walk returns keys from start (inclusive) that share keep as a prefix.
func walk(tx storage.Tx, start, keep []byte) ([][]byte, error) {
	cur, err := tx.Cursor(storage.KSProp)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close() }()
	if !cur.Seek(start) {
		return nil, cur.Close()
	}
	var out [][]byte
	for {
		k := cur.Key()
		if !bytes.HasPrefix(k, keep) {
			break
		}
		out = append(out, append([]byte(nil), k...))
		if !cur.Next() {
			break
		}
	}
	return out, cur.Close()
}

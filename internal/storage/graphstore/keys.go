package graphstore

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Catalog record kinds. Counters live in the catalog keyspace under a fixed name.
const (
	catProp      byte = 1
	catLabel     byte = 2
	catEdgeType  byte = 3
	catNextNode  byte = 4
	catNextEdge  byte = 5
	catNextProp  byte = 6
	catNextLabel byte = 7
	catNextType  byte = 8
	catPropRev   byte = 0x81
	catLabelRev  byte = 0x82
	catTypeRev   byte = 0x83
)

func idKey(id uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, id)
	return buf
}

func outKey(from uint64, typeID uint32, to, edgeID uint64) []byte {
	buf := make([]byte, 28)
	binary.BigEndian.PutUint64(buf[0:8], from)
	binary.BigEndian.PutUint32(buf[8:12], typeID)
	binary.BigEndian.PutUint64(buf[12:20], to)
	binary.BigEndian.PutUint64(buf[20:28], edgeID)
	return buf
}

func inKey(to uint64, typeID uint32, from, edgeID uint64) []byte {
	buf := make([]byte, 28)
	binary.BigEndian.PutUint64(buf[0:8], to)
	binary.BigEndian.PutUint32(buf[8:12], typeID)
	binary.BigEndian.PutUint64(buf[12:20], from)
	binary.BigEndian.PutUint64(buf[20:28], edgeID)
	return buf
}

func labelKey(labelID uint32, nodeID uint64) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[:4], labelID)
	binary.BigEndian.PutUint64(buf[4:], nodeID)
	return buf
}

func typeKey(typeID uint32, edgeID uint64) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[:4], typeID)
	binary.BigEndian.PutUint64(buf[4:], edgeID)
	return buf
}

func propIndexKey(propID uint32, v value.Value, nodeID uint64) []byte {
	prefix := propIndexPrefix(propID, v)
	buf := make([]byte, len(prefix)+8)
	copy(buf, prefix)
	binary.BigEndian.PutUint64(buf[len(prefix):], nodeID)
	return buf
}

func propIndexPrefix(propID uint32, v value.Value) []byte {
	ek := value.EncodeKey(v)
	buf := make([]byte, 4+len(ek))
	binary.BigEndian.PutUint32(buf[:4], propID)
	copy(buf[4:], ek)
	return buf
}

func catalogKey(kind byte, name string) []byte {
	buf := []byte{kind}
	buf = binary.AppendUvarint(buf, uint64(len(name)))
	buf = append(buf, name...)
	return buf
}

func revKey(kind byte, id uint32) []byte {
	buf := make([]byte, 5)
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:], id)
	return buf
}

func parseAdj(k []byte) (a uint64, typeID uint32, b, edgeID uint64, ok bool) {
	if len(k) != 28 {
		return 0, 0, 0, 0, false
	}
	a = binary.BigEndian.Uint64(k[0:8])
	typeID = binary.BigEndian.Uint32(k[8:12])
	b = binary.BigEndian.Uint64(k[12:20])
	edgeID = binary.BigEndian.Uint64(k[20:28])
	return a, typeID, b, edgeID, true
}

func readCounter(tx storage.Tx, kind byte) (uint64, error) {
	raw, err := tx.Get(storage.KSCatalog, catalogKey(kind, ""))
	if errors.Is(err, storage.ErrNotFound) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	if len(raw) != 8 {
		return 0, corrupt("counter")
	}
	return binary.BigEndian.Uint64(raw), nil
}

func writeCounter(tx storage.Tx, kind byte, next uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, next)
	return tx.Put(storage.KSCatalog, catalogKey(kind, ""), buf)
}

func alloc(tx storage.Tx, kind byte) (uint64, error) {
	next, err := readCounter(tx, kind)
	if err != nil {
		return 0, err
	}
	if err := writeCounter(tx, kind, next+1); err != nil {
		return 0, err
	}
	return next, nil
}

func putName(tx storage.Tx, kind, rev byte, name string, id uint32) error {
	idb := make([]byte, 4)
	binary.BigEndian.PutUint32(idb, id)
	if err := tx.Put(storage.KSCatalog, catalogKey(kind, name), idb); err != nil {
		return err
	}
	return tx.Put(storage.KSCatalog, revKey(rev, id), []byte(name))
}

func intern(tx storage.Tx, kind, rev, counter byte, name string) (uint32, error) {
	raw, err := tx.Get(storage.KSCatalog, catalogKey(kind, name))
	if err == nil {
		if len(raw) != 4 {
			return 0, corrupt("catalog id")
		}
		return binary.BigEndian.Uint32(raw), nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return 0, err
	}
	next, err := readCounter(tx, counter)
	if err != nil {
		return 0, err
	}
	if next == 0 {
		next = 1
	}
	if next > math.MaxUint32 {
		return 0, corrupt("catalog id space")
	}
	id := uint32(next)
	if err := putName(tx, kind, rev, name, id); err != nil {
		return 0, err
	}
	if err := writeCounter(tx, counter, next+1); err != nil {
		return 0, err
	}
	return id, nil
}

func catName(tx storage.Tx, rev byte, id uint32) (string, bool, error) {
	if id == 0 {
		return "", false, nil
	}
	raw, err := tx.Get(storage.KSCatalog, revKey(rev, id))
	if errors.Is(err, storage.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

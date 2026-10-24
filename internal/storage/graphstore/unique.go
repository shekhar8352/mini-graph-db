package graphstore

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Unique is a node property that must have one live value at commit.
type Unique struct {
	Name  string
	Label string
	Prop  string
}

// PutUnique stores a node UNIQUE constraint under name.
func PutUnique(tx storage.Tx, name, label, prop string) error {
	if name == "" || label == "" || prop == "" {
		return invalidf("constraint needs a name, label, and property")
	}
	return tx.Put(storage.KSCatalog, catalogKey(catUniqueDef, name), encodeUnique(label, prop))
}

// DropUnique removes a UNIQUE constraint and its index keys.
// A missing name leaves the catalog unchanged and reports false.
func DropUnique(tx storage.Tx, name string) (bool, error) {
	key := catalogKey(catUniqueDef, name)
	raw, err := tx.Get(storage.KSCatalog, key)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	label, prop, err := decodeUnique(raw)
	if err != nil {
		return false, err
	}
	keys, err := scanPrefix(tx, storage.KSCatalog, uniquePrefix(label, prop))
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if err := tx.Delete(storage.KSCatalog, k); err != nil {
			return false, err
		}
	}
	if err := tx.Delete(storage.KSCatalog, key); err != nil {
		return false, err
	}
	return true, nil
}

// ListUniques returns every stored node UNIQUE constraint.
func ListUniques(tx storage.Tx) ([]Unique, error) {
	keys, err := scanPrefix(tx, storage.KSCatalog, []byte{catUniqueDef})
	if err != nil {
		return nil, err
	}
	out := make([]Unique, 0, len(keys))
	for _, k := range keys {
		name, ok := parseCatalogName(k, catUniqueDef)
		if !ok {
			continue
		}
		raw, err := tx.Get(storage.KSCatalog, k)
		if err != nil {
			return nil, err
		}
		label, prop, err := decodeUnique(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, Unique{Name: name, Label: label, Prop: prop})
	}
	return out, nil
}

// UniqueKey is the catalog key for one live property value.
func UniqueKey(label, prop string, v value.Value) []byte {
	buf := uniquePrefix(label, prop)
	return append(buf, value.EncodeKey(v)...)
}

// UniqueIndexKeys returns every live unique-index key.
func UniqueIndexKeys(tx storage.Tx) ([][]byte, error) {
	return scanPrefix(tx, storage.KSCatalog, []byte{catUniqueIdx})
}

// UniqueOwner reads the node id stored at a unique-index key.
func UniqueOwner(tx storage.Tx, key []byte) (uint64, bool, error) {
	raw, err := tx.Get(storage.KSCatalog, key)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(raw) != 8 {
		return 0, false, corrupt("unique key")
	}
	return binary.BigEndian.Uint64(raw), true, nil
}

func uniquePrefix(label, prop string) []byte {
	buf := []byte{catUniqueIdx}
	buf = binary.AppendUvarint(buf, uint64(len(label)))
	buf = append(buf, label...)
	buf = binary.AppendUvarint(buf, uint64(len(prop)))
	buf = append(buf, prop...)
	return buf
}

func encodeUnique(label, prop string) []byte {
	return append(append([]byte(label), 0), prop...)
}

func decodeUnique(raw []byte) (string, string, error) {
	i := bytes.IndexByte(raw, 0)
	if i <= 0 || i == len(raw)-1 {
		return "", "", corrupt("unique constraint")
	}
	return string(raw[:i]), string(raw[i+1:]), nil
}

func parseCatalogName(k []byte, kind byte) (string, bool) {
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

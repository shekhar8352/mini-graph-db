package catalog

import (
	"bytes"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

func scanKind(tx storage.Tx, kind byte) ([][]byte, error) {
	cur, err := tx.Cursor(storage.KSCatalog)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close() }()
	prefix := []byte{kind}
	if !cur.Seek(prefix) {
		return nil, cur.Close()
	}
	var out [][]byte
	for {
		k := cur.Key()
		if !bytes.HasPrefix(k, prefix) {
			break
		}
		out = append(out, append([]byte(nil), k...))
		if !cur.Next() {
			break
		}
	}
	return out, cur.Close()
}

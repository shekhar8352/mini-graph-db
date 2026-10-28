package catalog

import (
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// UniqueLockKey is the catalog key DeferUnique uses for one declared unique
// index value. It does not include the entity id, so two writers of the same
// value conflict at commit.
func UniqueLockKey(indexID uint32, vals []value.Value) []byte {
	buf := []byte{KindUniqueLock}
	buf = binary.AppendUvarint(buf, uint64(indexID))
	return append(buf, value.EncodeComposite(vals...)...)
}

// UniqueLockPrefix is the scan prefix for every unique-lock key.
func UniqueLockPrefix() []byte {
	return []byte{KindUniqueLock}
}

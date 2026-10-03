package txn

import (
	"encoding/binary"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

const (
	// formatVersion is the MVCC catalog stamp. A newer stamp is refused.
	// Page and WAL format versions stay 1; this stamp lives in KSVersion.
	formatVersion byte = 1

	// headMagic marks a versioned head value. Records without it are legacy
	// payloads from before the transaction manager, treated as createTS 0.
	headMagic byte = 0xA5

	headHeader = 17

	chainMark byte = 0x01
)

// metaKey is the oracle record in KSVersion. Chain keys start with chainMark,
// so the two ranges do not overlap.
var metaKey = []byte{0x00}

// Visible reports whether a committed version belongs to snapshot.
// deleteTS 0 means the version has not been superseded or deleted.
// A read at S sees the version when createTS <= S and the version is still
// live at S.
func Visible(createTS, deleteTS, snapshot uint64) bool {
	if createTS > snapshot {
		return false
	}
	if deleteTS != 0 && deleteTS <= snapshot {
		return false
	}
	return true
}

type head struct {
	create  uint64
	delete  uint64
	payload []byte
	legacy  bool
}

func encodeHead(create, deleteTS uint64, payload []byte) []byte {
	buf := make([]byte, headHeader+len(payload))
	buf[0] = headMagic
	binary.BigEndian.PutUint64(buf[1:9], create)
	binary.BigEndian.PutUint64(buf[9:17], deleteTS)
	copy(buf[headHeader:], payload)
	return buf
}

func decodeHead(b []byte) head {
	if len(b) < headHeader || b[0] != headMagic {
		return head{payload: clone(b), legacy: true}
	}
	return head{
		create:  binary.BigEndian.Uint64(b[1:9]),
		delete:  binary.BigEndian.Uint64(b[9:17]),
		payload: clone(b[headHeader:]),
	}
}

func encodeChain(deleteTS uint64, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(buf[:8], deleteTS)
	copy(buf[8:], payload)
	return buf
}

func decodeChain(b []byte) (head, error) {
	if len(b) < 8 {
		return head{}, gerr.New(gerr.Corruption, "version chain record is truncated")
	}
	return head{
		delete:  binary.BigEndian.Uint64(b[:8]),
		payload: clone(b[8:]),
	}, nil
}

func chainPrefix(ks storage.Keyspace, key []byte) []byte {
	buf := make([]byte, 6+len(key))
	buf[0] = chainMark
	buf[1] = byte(ks)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(key)))
	copy(buf[6:], key)
	return buf
}

func chainKey(ks storage.Keyspace, key []byte, createTS uint64) []byte {
	p := chainPrefix(ks, key)
	buf := make([]byte, len(p)+8)
	copy(buf, p)
	binary.BigEndian.PutUint64(buf[len(p):], createTS)
	return buf
}

type oracle struct {
	nextTxn  uint64
	commitTS uint64
}

func encodeOracle(o oracle) []byte {
	buf := make([]byte, 17)
	buf[0] = formatVersion
	binary.BigEndian.PutUint64(buf[1:9], o.nextTxn)
	binary.BigEndian.PutUint64(buf[9:17], o.commitTS)
	return buf
}

func decodeOracle(b []byte) (oracle, error) {
	if len(b) != 17 {
		return oracle{}, gerr.New(gerr.Corruption, "mvcc oracle record has the wrong length")
	}
	if b[0] > formatVersion {
		return oracle{}, gerr.Newf(gerr.InvalidArgument, "database mvcc format %d is newer than %d; refusing to open", b[0], formatVersion)
	}
	if b[0] != formatVersion {
		return oracle{}, gerr.Newf(gerr.Corruption, "mvcc oracle format %d is not supported", b[0])
	}
	return oracle{
		nextTxn:  binary.BigEndian.Uint64(b[1:9]),
		commitTS: binary.BigEndian.Uint64(b[9:17]),
	}, nil
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func payloadCopy(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return clone(b)
}

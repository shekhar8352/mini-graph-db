package txn

import (
	"bytes"
	"errors"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

func migrate(eng storage.Engine) error {
	_, ok, err := readOracle(eng)
	if err != nil || ok {
		return err
	}
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		return err
	}
	type pending struct {
		ks      storage.Keyspace
		key     []byte
		payload []byte
	}
	var wrap []pending
	var maxCreate uint64
	var saw bool
	for _, ks := range userKeyspaces {
		err := eachRaw(tx, ks, func(key, val []byte) error {
			h := decodeHead(val)
			saw = true
			if !h.legacy {
				if h.create > maxCreate {
					maxCreate = h.create
				}
				return nil
			}
			wrap = append(wrap, pending{ks: ks, key: key, payload: h.payload})
			return nil
		})
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, item := range wrap {
		if err := tx.Put(item.ks, item.key, encodeHead(1, 0, item.payload)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if len(wrap) > 0 && maxCreate < 1 {
		maxCreate = 1
	}
	o := oracle{nextTxn: 1, commitTS: 0}
	if saw {
		if maxCreate == 0 {
			maxCreate = 1
		}
		o.commitTS = maxCreate
		o.nextTxn = maxCreate + 1
	}
	if err := tx.Put(storage.KSVersion, metaKey, encodeOracle(o)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

func loadOracle(eng storage.Engine) (oracle, error) {
	o, ok, err := readOracle(eng)
	if err != nil {
		return oracle{}, err
	}
	if !ok {
		return oracle{}, gerr.New(gerr.Internal, "mvcc oracle is missing after open")
	}
	return o, nil
}

func readOracle(eng storage.Engine) (oracle, bool, error) {
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return oracle{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := tx.Get(storage.KSVersion, metaKey)
	if isNotFound(err) {
		return oracle{}, false, nil
	}
	if err != nil {
		return oracle{}, false, err
	}
	o, err := decodeOracle(raw)
	if err != nil {
		return oracle{}, false, err
	}
	return o, true, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

func (m *Manager) countLive() (int, error) {
	tx, err := m.eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for ks := range m.spaces {
		err := eachHead(tx, ks, func(_ []byte, h head) error {
			if h.delete == 0 {
				n++
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

func readHead(tx storage.Tx, ks storage.Keyspace, key []byte) (head, bool, error) {
	raw, err := tx.Get(ks, key)
	if isNotFound(err) {
		return head{}, false, nil
	}
	if err != nil {
		return head{}, false, err
	}
	return decodeHead(raw), true, nil
}

func loadChain(tx storage.Tx, ks storage.Keyspace, key []byte) ([]head, error) {
	prefix := chainPrefix(ks, key)
	cur, err := tx.Cursor(storage.KSVersion)
	if err != nil {
		return nil, err
	}
	var out []head
	ok := cur.Seek(prefix)
	for ok {
		k := cur.Key()
		if !bytes.HasPrefix(k, prefix) || len(k) != len(prefix)+8 {
			break
		}
		h, err := decodeChain(cur.Value())
		if err != nil {
			_ = cur.Close()
			return nil, err
		}
		h.create = u64(k[len(prefix):])
		out = append(out, h)
		ok = cur.Next()
	}
	if err := cur.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

func visiblePayload(h head, ok bool, chain []head, snap uint64) ([]byte, bool) {
	if ok && Visible(h.create, h.delete, snap) {
		return payloadCopy(h.payload), true
	}
	if ok && h.create <= snap && h.delete != 0 && h.delete <= snap {
		return nil, false
	}
	var best *head
	for i := range chain {
		v := &chain[i]
		if !Visible(v.create, v.delete, snap) {
			continue
		}
		if best == nil || v.create > best.create {
			best = v
		}
	}
	if best == nil {
		return nil, false
	}
	return payloadCopy(best.payload), true
}

func eachRaw(tx storage.Tx, ks storage.Keyspace, fn func(key, val []byte) error) error {
	cur, err := tx.Cursor(ks)
	if err != nil {
		return err
	}
	ok := cur.Seek(nil)
	for ok {
		if err := fn(clone(cur.Key()), clone(cur.Value())); err != nil {
			_ = cur.Close()
			return err
		}
		ok = cur.Next()
	}
	return cur.Close()
}

func eachHead(tx storage.Tx, ks storage.Keyspace, fn func(key []byte, h head) error) error {
	return eachRaw(tx, ks, func(key, val []byte) error {
		return fn(key, decodeHead(val))
	})
}

func eachChain(tx storage.Tx, fn func(key []byte, h head) error) error {
	cur, err := tx.Cursor(storage.KSVersion)
	if err != nil {
		return err
	}
	ok := cur.Seek([]byte{chainMark})
	for ok {
		k := cur.Key()
		if len(k) == 0 || k[0] != chainMark {
			break
		}
		h, err := decodeChain(cur.Value())
		if err != nil {
			_ = cur.Close()
			return err
		}
		if len(k) >= 8 {
			h.create = u64(k[len(k)-8:])
		}
		if err := fn(clone(k), h); err != nil {
			_ = cur.Close()
			return err
		}
		ok = cur.Next()
	}
	return cur.Close()
}

func u64(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}

func checkKS(ks storage.Keyspace) error {
	if ks == storage.KSVersion {
		return gerr.New(gerr.InvalidArgument, "keyspace V is reserved for versions")
	}
	return nil
}

// Package enginetest is the black-box conformance suite every storage.Engine
// must pass. The disk engine runs the same tests once it exists.
package enginetest

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

// OpenFunc constructs a fresh engine for one test.
type OpenFunc func() storage.Engine

// Run executes the conformance suite against open.
func Run(t *testing.T, open OpenFunc) {
	t.Helper()
	t.Run("put get delete", func(t *testing.T) { testPutGetDelete(t, open) })
	t.Run("ordering", func(t *testing.T) { testOrdering(t, open) })
	t.Run("cursor", func(t *testing.T) { testCursor(t, open) })
	t.Run("keyspaces", func(t *testing.T) { testKeyspaces(t, open) })
	t.Run("isolation", func(t *testing.T) { testIsolation(t, open) })
	t.Run("snapshot", func(t *testing.T) { testSnapshot(t, open) })
	t.Run("read only", func(t *testing.T) { testReadOnly(t, open) })
	t.Run("crash", func(t *testing.T) { testCrash(t, open) })
	t.Run("hooks", func(t *testing.T) { testHooks(t, open) })
	t.Run("sync stats close", func(t *testing.T) { testSyncStatsClose(t, open) })
	t.Run("concurrent", func(t *testing.T) { testConcurrent(t, open) })
}

func testPutGetDelete(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })

	tx := begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("b"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := tx.Get(storage.KSNode, []byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("get a: %q %v", got, err)
	}
	got, err = tx.Get(storage.KSNode, []byte("b"))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty value: %#v %v", got, err)
	}
	if err := tx.Delete(storage.KSNode, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Get(storage.KSNode, []byte("a")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted key: %v", err)
	}
	if err := tx.Delete(storage.KSNode, []byte("missing")); err != nil {
		t.Fatal(err)
	}
	commit(t, tx)

	tx = begin(t, eng, true)
	got, err = tx.Get(storage.KSNode, []byte("b"))
	if err != nil || len(got) != 0 {
		t.Fatalf("committed empty: %#v %v", got, err)
	}
	if _, err := tx.Get(storage.KSNode, []byte("a")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete not committed: %v", err)
	}
	rollback(t, tx)
}

func testOrdering(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	keys := [][]byte{{0x02}, {0x01, 0xff}, {0x01}, {0x01, 0x00}, {0x00}}
	tx := begin(t, eng, false)
	for _, k := range keys {
		if err := tx.Put(storage.KSCatalog, k, k); err != nil {
			t.Fatal(err)
		}
	}
	commit(t, tx)

	want := [][]byte{{0x00}, {0x01}, {0x01, 0x00}, {0x01, 0xff}, {0x02}}
	tx = begin(t, eng, true)
	got := scan(t, tx, storage.KSCatalog, nil)
	if !sameKeys(got, want) {
		t.Fatalf("order: %v want %v", got, want)
	}
	rollback(t, tx)
}

func testCursor(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	tx := begin(t, eng, false)
	for _, k := range []string{"b", "d", "f"} {
		if err := tx.Put(storage.KSEdge, []byte(k), []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Put(storage.KSEdge, []byte{}, []byte("empty")); err != nil {
		t.Fatal(err)
	}
	commit(t, tx)

	tx = begin(t, eng, true)
	cur := cursor(t, tx, storage.KSEdge)
	if cur.Valid() {
		t.Fatal("cursor valid before seek")
	}
	if !cur.Seek(nil) || string(cur.Key()) != "" {
		t.Fatalf("seek nil: %q valid=%v", cur.Key(), cur.Valid())
	}
	if !cur.Next() || string(cur.Key()) != "b" {
		t.Fatalf("next: %q", cur.Key())
	}
	if !cur.Seek([]byte("c")) || string(cur.Key()) != "d" {
		t.Fatalf("seek c: %q", cur.Key())
	}
	if !cur.SeekReverse([]byte("c")) || string(cur.Key()) != "b" {
		t.Fatalf("seekreverse c: %q", cur.Key())
	}
	if !cur.SeekReverse(nil) || string(cur.Key()) != "f" {
		t.Fatalf("seekreverse nil: %q", cur.Key())
	}
	if cur.Next() {
		t.Fatal("next past end")
	}
	if !cur.Seek([]byte("b")) || !cur.Prev() || len(cur.Key()) != 0 {
		t.Fatalf("prev from b: %q", cur.Key())
	}
	if cur.Prev() {
		t.Fatal("prev past start")
	}
	if cur.Seek([]byte("z")) {
		t.Fatal("seek past end")
	}
	if err := cur.Close(); err != nil {
		t.Fatal(err)
	}
	if cur.Seek(nil) {
		t.Fatal("seek after close")
	}
	rollback(t, tx)

	tx = begin(t, eng, false)
	if err := tx.Put(storage.KSEdge, []byte("e"), []byte("e")); err != nil {
		t.Fatal(err)
	}
	cur = cursor(t, tx, storage.KSEdge)
	if !cur.Seek([]byte("e")) || string(cur.Value()) != "e" {
		t.Fatal("cursor missed own write")
	}
	if err := cur.Close(); err != nil {
		t.Fatal(err)
	}
	rollback(t, tx)
}

func testKeyspaces(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	tx := begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("k"), []byte("node")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSEdge, []byte("k"), []byte("edge")); err != nil {
		t.Fatal(err)
	}
	commit(t, tx)
	tx = begin(t, eng, true)
	n, err := tx.Get(storage.KSNode, []byte("k"))
	if err != nil || string(n) != "node" {
		t.Fatalf("node: %q %v", n, err)
	}
	e, err := tx.Get(storage.KSEdge, []byte("k"))
	if err != nil || string(e) != "edge" {
		t.Fatalf("edge: %q %v", e, err)
	}
	if keys := scan(t, tx, storage.KSOut, nil); len(keys) != 0 {
		t.Fatalf("out keyspace: %v", keys)
	}
	rollback(t, tx)
}

func testIsolation(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })

	w := begin(t, eng, false)
	if err := w.Put(storage.KSNode, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	r := begin(t, eng, true)
	if _, err := r.Get(storage.KSNode, []byte("k")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("reader saw uncommitted write: %v", err)
	}
	got, err := w.Get(storage.KSNode, []byte("k"))
	if err != nil || string(got) != "v" {
		t.Fatalf("writer get: %q %v", got, err)
	}
	rollback(t, w)
	if _, err := r.Get(storage.KSNode, []byte("k")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("rollback leaked")
	}
	rollback(t, r)

	w = begin(t, eng, false)
	if err := w.Put(storage.KSNode, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	commit(t, w)
	r = begin(t, eng, true)
	got, err = r.Get(storage.KSNode, []byte("k"))
	if err != nil || string(got) != "v" {
		t.Fatalf("committed: %q %v", got, err)
	}
	rollback(t, r)
}

func testSnapshot(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	r := begin(t, eng, true)
	w := begin(t, eng, false)
	if err := w.Put(storage.KSProp, []byte("n"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	commit(t, w)
	if _, err := r.Get(storage.KSProp, []byte("n")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("reader observed a commit that started after it")
	}
	rollback(t, r)
	r = begin(t, eng, true)
	got, err := r.Get(storage.KSProp, []byte("n"))
	if err != nil || string(got) != "1" {
		t.Fatalf("new reader: %q %v", got, err)
	}
	rollback(t, r)
}

func testReadOnly(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	tx := begin(t, eng, true)
	if err := tx.Put(storage.KSNode, []byte("a"), []byte("b")); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatalf("put: %v", err)
	}
	if err := tx.Delete(storage.KSNode, []byte("a")); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatalf("delete: %v", err)
	}
	commit(t, tx)
	if st := eng.Stats(); st.Keys != 0 {
		t.Fatalf("read-only commit wrote keys: %+v", st)
	}
}

func testCrash(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })
	tx := begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("kept"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	commit(t, tx)

	tx = begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("lost"), []byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Crash(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, storage.ErrDone) {
		t.Fatalf("commit after crash: %v", err)
	}
	tx = begin(t, eng, true)
	if _, err := tx.Get(storage.KSNode, []byte("lost")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("crash published uncommitted data")
	}
	got, err := tx.Get(storage.KSNode, []byte("kept"))
	if err != nil || string(got) != "yes" {
		t.Fatalf("committed data lost: %q %v", got, err)
	}
	rollback(t, tx)
}

func testHooks(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })

	eng.SetHooks(storage.Hooks{BeforeCommit: func() error { return errors.New("boom") }})
	tx := begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil || err.Error() != "boom" {
		t.Fatalf("before commit: %v", err)
	}
	r := begin(t, eng, true)
	if _, err := r.Get(storage.KSNode, []byte("k")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("before-commit hook published the write")
	}
	rollback(t, r)
	rollback(t, tx)

	eng.SetHooks(storage.Hooks{})
	tx = begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	commit(t, tx)

	eng.SetHooks(storage.Hooks{AfterCommit: func() error { return errors.New("ack") }})
	tx = begin(t, eng, false)
	if err := tx.Put(storage.KSNode, []byte("k2"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil || err.Error() != "ack" {
		t.Fatalf("after commit: %v", err)
	}
	r = begin(t, eng, true)
	got, err := r.Get(storage.KSNode, []byte("k2"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("after-commit hook hid a published write: %q %v", got, err)
	}
	rollback(t, r)
}

func testSyncStatsClose(t *testing.T, open OpenFunc) {
	eng := open()
	tx := begin(t, eng, false)
	if err := tx.Put(storage.KSLabel, []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSLabel, []byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if st := eng.Stats(); st.Keys != 0 {
		t.Fatalf("stats counted uncommitted keys: %+v", st)
	}
	commit(t, tx)
	if err := eng.Sync(); err != nil {
		t.Fatal(err)
	}
	st := eng.Stats()
	if st.Keys != 2 || st.Commits == 0 {
		t.Fatalf("stats: %+v", st)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Begin(storage.TxOptions{}); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("begin after close: %v", err)
	}
	if err := eng.Sync(); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("sync after close: %v", err)
	}
}

func testConcurrent(t *testing.T, open OpenFunc) {
	eng := open()
	t.Cleanup(func() { closeEng(t, eng) })

	release := make(chan struct{})
	ready := make(chan struct{})
	go func() {
		tx, err := eng.Begin(storage.TxOptions{})
		if err != nil {
			t.Error(err)
			close(ready)
			return
		}
		if err := tx.Put(storage.KSNode, []byte("secret"), []byte("x")); err != nil {
			t.Error(err)
		}
		close(ready)
		<-release
		if err := tx.Rollback(); err != nil {
			t.Error(err)
		}
	}()
	<-ready

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tx.Get(storage.KSNode, []byte("secret")); !errors.Is(err, storage.ErrNotFound) {
				t.Errorf("concurrent reader saw uncommitted write: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(release)
}

func begin(t *testing.T, eng storage.Engine, ro bool) storage.Tx {
	t.Helper()
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: ro})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func commit(t *testing.T, tx storage.Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func rollback(t *testing.T, tx storage.Tx) {
	t.Helper()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func closeEng(t *testing.T, eng storage.Engine) {
	t.Helper()
	if err := eng.Close(); err != nil {
		t.Error(err)
	}
}

func cursor(t *testing.T, tx storage.Tx, ks storage.Keyspace) storage.Cursor {
	t.Helper()
	cur, err := tx.Cursor(ks)
	if err != nil {
		t.Fatal(err)
	}
	return cur
}

func scan(t *testing.T, tx storage.Tx, ks storage.Keyspace, prefix []byte) [][]byte {
	t.Helper()
	cur := cursor(t, tx, ks)
	defer func() { _ = cur.Close() }()
	var out [][]byte
	if !cur.Seek(prefix) {
		return nil
	}
	for {
		k := cur.Key()
		if prefix != nil && !bytes.HasPrefix(k, prefix) {
			break
		}
		out = append(out, k)
		if !cur.Next() {
			break
		}
	}
	return out
}

func sameKeys(got, want [][]byte) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			return false
		}
	}
	return true
}

package disk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
)

func newTree(t *testing.T, ks storage.Keyspace) (*Tree, *PageFile, *Pool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tree.db")
	pf, err := Create(path, Options{PageSize: MinPageSize})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(pf, 64)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := OpenTree(pf, pool, ks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_ = pf.Close()
	})
	return tree, pf, pool
}

func keyOf(n int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	return b[:]
}

func TestLeafPageRoundTrip(t *testing.T) {
	payload := make([]byte, 256)
	page := leafPage{
		id:    3,
		left:  1,
		right: 4,
		cells: []leafCell{
			{key: []byte("a"), val: []byte("alpha"), valLen: 5},
			{key: []byte("b"), val: []byte{}, valLen: 0},
			{key: []byte{0x00, 0xff}, overflow: 9, valLen: 100},
		},
	}
	if err := encodeLeaf(payload, page); err != nil {
		t.Fatal(err)
	}
	got, err := decodeLeaf(3, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.left != 1 || got.right != 4 || len(got.cells) != 3 {
		t.Fatalf("%+v", got)
	}
	if !bytes.Equal(got.cells[2].key, []byte{0x00, 0xff}) || got.cells[2].overflow != 9 || got.cells[2].valLen != 100 {
		t.Fatalf("overflow cell %+v", got.cells[2])
	}
	internal := internalPage{
		id:       8,
		keys:     [][]byte{[]byte("m"), []byte("z")},
		children: []PageID{2, 3, 5},
	}
	if err := encodeInternal(payload, internal); err != nil {
		t.Fatal(err)
	}
	inode, err := decodeInternal(8, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(inode.keys) != 2 || inode.children[2] != 5 || !bytes.Equal(inode.keys[0], []byte("m")) {
		t.Fatalf("%+v", inode)
	}
}

func TestTreePutGetDeleteAndScan(t *testing.T) {
	tree, _, _ := newTree(t, storage.KSNode)
	if _, err := tree.Get(keyOf(1)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	const n = 200
	for i := n; i >= 1; i-- {
		if err := tree.Put(keyOf(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tree.verify(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		got, err := tree.Get(keyOf(i))
		if err != nil || len(got) != 1 || got[0] != byte(i) {
			t.Fatalf("get %d: %v %v", i, got, err)
		}
	}
	cur, err := tree.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	if !cur.Seek(nil) {
		t.Fatal(cur.Err())
	}
	prev := -1
	count := 0
	for cur.Valid() {
		k := int(binary.BigEndian.Uint64(cur.Key()))
		if k <= prev {
			t.Fatalf("order %d then %d", prev, k)
		}
		prev = k
		count++
		if !cur.Next() && cur.Err() != nil {
			t.Fatal(cur.Err())
		}
	}
	if count != n {
		t.Fatalf("scanned %d", count)
	}
	if !cur.SeekReverse(nil) {
		t.Fatal(cur.Err())
	}
	if int(binary.BigEndian.Uint64(cur.Key())) != n {
		t.Fatalf("last %d", binary.BigEndian.Uint64(cur.Key()))
	}
	back := 0
	for cur.Valid() {
		back++
		if !cur.Prev() && cur.Err() != nil {
			t.Fatal(cur.Err())
		}
	}
	if back != n {
		t.Fatalf("reverse %d", back)
	}
	if cur.Seek(keyOf(0)) && int(binary.BigEndian.Uint64(cur.Key())) != 1 {
		t.Fatalf("seek before first")
	}
	if !cur.Seek(keyOf(50)) || int(binary.BigEndian.Uint64(cur.Key())) != 50 {
		t.Fatal("seek exact")
	}
	between := append(keyOf(50), 0x00)
	if !cur.Seek(between) || int(binary.BigEndian.Uint64(cur.Key())) != 51 {
		t.Fatal("seek between")
	}
	if cur.SeekReverse(keyOf(0)) {
		t.Fatal("seek reverse before first should miss")
	}
	if !cur.SeekReverse(between) || int(binary.BigEndian.Uint64(cur.Key())) != 50 {
		t.Fatal("seek reverse between")
	}
	if err := cur.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cur.Close(); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= n; i += 2 {
		if err := tree.Delete(keyOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tree.Delete(keyOf(1)); err != nil {
		t.Fatal(err)
	}
	if err := tree.verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Get(keyOf(1)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := tree.Get(keyOf(2)); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= n; i += 2 {
		if err := tree.Delete(keyOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tree.verify(); err != nil {
		t.Fatal(err)
	}
	root, err := tree.root()
	if err != nil || root != 0 {
		t.Fatalf("root %d %v", root, err)
	}
}

func TestTreeReplaceOverflowAndReopen(t *testing.T) {
	tree, pf, pool := newTree(t, storage.KSEdge)
	small := []byte("small")
	if err := tree.Put([]byte("k"), small); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0xab}, pf.PageSize()/4+50)
	if err := tree.Put([]byte("k"), big); err != nil {
		t.Fatal(err)
	}
	got, err := tree.Get([]byte("k"))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("overflow get %d %v", len(got), err)
	}
	if err := tree.Put([]byte("k"), small); err != nil {
		t.Fatal(err)
	}
	got, err = tree.Get([]byte("k"))
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("replaced %q %v", got, err)
	}
	empty := []byte{}
	if err := tree.Put([]byte{}, empty); err != nil {
		t.Fatal(err)
	}
	got, err = tree.Get([]byte{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty key %v %v", got, err)
	}
	if err := tree.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	if err := pool.FlushAll(); err != nil {
		t.Fatal(err)
	}
	path := pf.path
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pf.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	npool, err := NewPool(opened, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = npool.Close() }()
	again, err := OpenTree(opened, npool, storage.KSEdge)
	if err != nil {
		t.Fatal(err)
	}
	got, err = again.Get([]byte{})
	if err != nil || len(got) != 0 {
		t.Fatalf("reopen %v %v", got, err)
	}
	if _, err := again.Get([]byte("k")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	root, err := again.root()
	if err != nil || root == 0 {
		t.Fatal(err)
	}
	info, err := opened.ReadPage(root)
	if err != nil || info.Type != TypeTreeLeaf {
		t.Fatalf("root type %+v %v", info, err)
	}
}

func TestTreeTwoKeyspaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tree.db")
	pf, err := Create(path, Options{PageSize: MinPageSize})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(pf, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_ = pf.Close()
	})
	a, err := OpenTree(pf, pool, storage.KSNode)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenTree(pf, pool, storage.KSLabel)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put([]byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	got, err := a.Get([]byte("a"))
	if err != nil || string(got) != "1" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = b.Get([]byte("a"))
	if err != nil || string(got) != "2" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestTreeRejectsHugeKey(t *testing.T) {
	tree, _, _ := newTree(t, storage.KSCatalog)
	key := bytes.Repeat([]byte{1}, 10000)
	if err := tree.Put(key, []byte{1}); !gerr.IsCode(err, gerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestTreeRandomMatchesMap(t *testing.T) {
	tree, _, _ := newTree(t, storage.KSProp)
	const ops = 1_000_000
	model := map[string][]byte{}
	keys := make([][]byte, 0, 4096)
	seed := uint64(0xC0FFEE)
	next := func() uint64 {
		seed = seed*6364136223846793005 + 1
		return seed
	}
	for i := 0; i < ops; i++ {
		roll := next() % 10
		switch {
		case roll < 7 || len(keys) == 0:
			k := keyOf(int(next() % 20000))
			v := []byte{byte(next()), byte(i)}
			if err := tree.Put(k, v); err != nil {
				t.Fatal(err)
			}
			s := string(k)
			if _, ok := model[s]; !ok {
				keys = append(keys, append([]byte(nil), k...))
			}
			model[s] = append([]byte(nil), v...)
		case roll < 9:
			k := keys[int(next()%uint64(len(keys)))]
			if err := tree.Delete(k); err != nil {
				t.Fatal(err)
			}
			delete(model, string(k))
		default:
			k := keys[int(next()%uint64(len(keys)))]
			got, err := tree.Get(k)
			want, ok := model[string(k)]
			if !ok {
				if !errors.Is(err, storage.ErrNotFound) {
					t.Fatalf("get deleted: %v", err)
				}
				continue
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("get mismatch %v %v", got, err)
			}
		}
		if i > 0 && i%100000 == 0 {
			if err := tree.verify(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tree.verify(); err != nil {
		t.Fatal(err)
	}
	cur, err := tree.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cur.Close() }()
	seen := map[string]struct{}{}
	if cur.Seek(nil) {
		for cur.Valid() {
			s := string(cur.Key())
			if _, dup := seen[s]; dup {
				t.Fatalf("duplicate %q", s)
			}
			seen[s] = struct{}{}
			want, ok := model[s]
			if !ok || !bytes.Equal(cur.Value(), want) {
				t.Fatalf("scan %q", s)
			}
			if !cur.Next() && cur.Err() != nil {
				t.Fatal(cur.Err())
			}
		}
	}
	if cur.Err() != nil {
		t.Fatal(cur.Err())
	}
	if len(seen) != len(model) {
		t.Fatalf("scan %d model %d", len(seen), len(model))
	}
}

func TestTreeConcurrentReaders(t *testing.T) {
	tree, _, _ := newTree(t, storage.KSNode)
	for i := 0; i < 500; i++ {
		if err := tree.Put(keyOf(i), keyOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errc := make(chan error, 8)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				k := keyOf(i % 800)
				_, err := tree.Get(k)
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					errc <- err
					return
				}
				cur, err := tree.Cursor()
				if err != nil {
					errc <- err
					return
				}
				if cur.Seek(k) && cur.Err() != nil {
					errc <- cur.Err()
					_ = cur.Close()
					return
				}
				_ = cur.Next()
				if err := cur.Close(); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			k := keyOf(500 + i%300)
			if i%3 == 0 {
				if err := tree.Delete(k); err != nil {
					errc <- err
					return
				}
			} else if err := tree.Put(k, []byte{byte(i)}); err != nil {
				errc <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if err := tree.verify(); err != nil {
		t.Fatal(err)
	}
}

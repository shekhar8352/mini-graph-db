package consistency

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
	"github.com/shekhar8352/mini-graph-db/internal/txn"
)

const histories = 10000

type opKind int

const (
	opRead opKind = iota
	opWrite
)

type op struct {
	kind  opKind
	key   string
	val   string
	found bool
}

type rec struct {
	snap      uint64
	commitTS  uint64
	committed bool
	ops       []op
}

func TestSnapshotIsolation(t *testing.T) {
	eng := memory.Open()
	m, err := txn.Open(eng, txn.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	seed, err := m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := seed.Put(storage.KSNode, []byte(k), []byte("0")); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	base := seed.(*txn.Tx).CommitTS()

	var seq atomic.Uint64
	var mu sync.Mutex
	hist := make([]rec, 0, histories)
	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			state := id*1_000_003 + 17
			for {
				n := seq.Add(1)
				if n > histories {
					return
				}
				r, err := runHistory(m, keys, &state, int(n))
				if err != nil {
					errCh <- err
					return
				}
				mu.Lock()
				hist = append(hist, r)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if len(hist) != histories {
		t.Fatalf("histories %d", len(hist))
	}
	if err := checkSI(hist, keys, base); err != nil {
		t.Fatal(err)
	}
}

func runHistory(m *txn.Manager, keys []string, state *int, n int) (rec, error) {
	ro := next(state)%10 == 0
	tx, err := m.Begin(storage.TxOptions{ReadOnly: ro})
	if err != nil {
		return rec{}, err
	}
	mtv, ok := tx.(*txn.Tx)
	if !ok {
		_ = tx.Rollback()
		return rec{}, fmt.Errorf("begin returned %T", tx)
	}
	out := rec{snap: mtv.Snapshot()}
	steps := 1 + next(state)%4
	for i := 0; i < steps; i++ {
		key := keys[next(state)%len(keys)]
		if ro || next(state)%2 == 0 {
			got, err := tx.Get(storage.KSNode, []byte(key))
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				_ = tx.Rollback()
				return rec{}, err
			}
			out.ops = append(out.ops, op{kind: opRead, key: key, val: string(got), found: err == nil})
			continue
		}
		val := fmt.Sprintf("%d-%d", n, i)
		if err := tx.Put(storage.KSNode, []byte(key), []byte(val)); err != nil {
			_ = tx.Rollback()
			return rec{}, err
		}
		out.ops = append(out.ops, op{kind: opWrite, key: key, val: val, found: true})
	}
	if next(state)%10 == 0 {
		if err := tx.Rollback(); err != nil {
			return rec{}, err
		}
		return out, nil
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		if gerr.IsCode(err, gerr.Conflict) {
			return out, nil
		}
		return rec{}, err
	}
	out.committed = true
	out.commitTS = mtv.CommitTS()
	return out, nil
}

func checkSI(hist []rec, keys []string, base uint64) error {
	model := map[string][]siVer{}
	type writer struct {
		snap uint64
		cts  uint64
	}
	writers := map[string][]writer{}
	for _, key := range keys {
		model[key] = []siVer{{cts: base, val: "0"}}
		writers[key] = []writer{{cts: base}}
	}
	for _, h := range hist {
		if !h.committed {
			continue
		}
		seen := map[string]struct{}{}
		for _, step := range h.ops {
			if step.kind != opWrite {
				continue
			}
			if _, ok := seen[step.key]; ok {
				continue
			}
			seen[step.key] = struct{}{}
			model[step.key] = append(model[step.key], siVer{cts: h.commitTS, val: lastWrite(h.ops, step.key)})
			writers[step.key] = append(writers[step.key], writer{snap: h.snap, cts: h.commitTS})
		}
	}
	for key, ws := range writers {
		for i := 1; i < len(ws); i++ {
			j := i
			for j > 0 && ws[j].cts < ws[j-1].cts {
				ws[j], ws[j-1] = ws[j-1], ws[j]
				j--
			}
		}
		for i := 1; i < len(ws); i++ {
			if ws[i].snap < ws[i-1].cts {
				return fmt.Errorf("lost update on %s: commit %d snapshot %d follows commit %d", key, ws[i].cts, ws[i].snap, ws[i-1].cts)
			}
		}
	}
	for i, h := range hist {
		own := map[string]string{}
		for _, step := range h.ops {
			if step.kind == opWrite {
				own[step.key] = step.val
				continue
			}
			if val, ok := own[step.key]; ok {
				if !step.found || step.val != val {
					return fmt.Errorf("history %d read-your-writes %s: got %q found=%v", i, step.key, step.val, step.found)
				}
				continue
			}
			val, found := at(model[step.key], h.snap)
			if found != step.found || (found && val != step.val) {
				return fmt.Errorf("history %d snapshot %d key %s: got %q found=%v want %q found=%v", i, h.snap, step.key, step.val, step.found, val, found)
			}
		}
	}
	return nil
}

func lastWrite(ops []op, key string) string {
	var val string
	for _, step := range ops {
		if step.kind == opWrite && step.key == key {
			val = step.val
		}
	}
	return val
}

type siVer struct {
	cts uint64
	val string
}

func at(vers []siVer, snap uint64) (string, bool) {
	var best uint64
	var val string
	found := false
	for _, v := range vers {
		if v.cts <= snap && (!found || v.cts >= best) {
			best = v.cts
			val = v.val
			found = true
		}
	}
	return val, found
}

func next(state *int) int {
	*state = *state*1103515245 + 12345
	if *state < 0 {
		return -*state
	}
	return *state
}

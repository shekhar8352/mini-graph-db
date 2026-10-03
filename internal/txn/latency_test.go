package txn

import (
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/memory"
)

func TestReaderLatencyUnderBulkWriter(t *testing.T) {
	m, err := Open(memory.Open(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	tx, err := m.Begin(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(storage.KSNode, []byte("r"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()

	base := sampleReads(t, m)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w, err := m.Begin(storage.TxOptions{})
		if err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 20000; i++ {
			select {
			case <-stop:
				_ = w.Rollback()
				return
			default:
			}
			key := []byte{byte(i), byte(i >> 8), byte(i >> 16)}
			if err := w.Put(storage.KSEdge, key, key); err != nil {
				t.Error(err)
				_ = w.Rollback()
				return
			}
			if i%32 == 0 {
				runtime.Gosched()
			}
		}
		_ = w.Rollback()
	}()
	during := sampleReads(t, m)
	close(stop)
	wg.Wait()
	if during > base*2 && base > 0 {
		t.Fatalf("p99 read %s during an uncommitted bulk write, baseline %s", during, base)
	}

	stop = make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		// One-key commits, then a pause, so the sample is not the engine
		// lock held for the whole measurement. The bulk writer above never
		// commits during the sample.
		for {
			select {
			case <-stop:
				return
			default:
			}
			w, err := m.Begin(storage.TxOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if err := w.Put(storage.KSEdge, []byte("w"), []byte("x")); err != nil {
				t.Error(err)
				_ = w.Rollback()
				return
			}
			if err := w.Commit(); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	committing := sampleReads(t, m)
	close(stop)
	wg.Wait()
	if committing > base*2 && base > 0 {
		t.Fatalf("p99 read %s while another transaction commits, baseline %s", committing, base)
	}
}

func sampleReads(t *testing.T, m *Manager) time.Duration {
	t.Helper()
	const samples = 30
	const batch = 1500
	durs := make([]time.Duration, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		tx, err := m.Begin(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < batch; j++ {
			if _, err := tx.Get(storage.KSNode, []byte("r")); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		durs[i] = time.Since(start)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	idx := (len(durs) - 1) * 99 / 100
	return durs[idx]
}

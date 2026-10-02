package bench_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/disk"
)

func openBench(b *testing.B) *disk.Engine {
	b.Helper()
	eng, err := disk.OpenEngine(b.TempDir(), disk.EngineOptions{
		CheckpointInterval: -1,
		CheckpointBytes:    -1,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = eng.Close() })
	return eng
}

func u64Key(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

func commitPut(b *testing.B, eng *disk.Engine, ks storage.Keyspace, key, val []byte) {
	b.Helper()
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		b.Fatal(err)
	}
	if err := tx.Put(ks, key, val); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

func loadSequential(b *testing.B, eng *disk.Engine, n int) {
	b.Helper()
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		b.Fatal(err)
	}
	val := []byte("v")
	for i := 0; i < n; i++ {
		if err := tx.Put(storage.KSNode, u64Key(uint64(i)), val); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkDiskSequentialInsert commits one key per transaction.
func BenchmarkDiskSequentialInsert(b *testing.B) {
	eng := openBench(b)
	val := []byte("v")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		commitPut(b, eng, storage.KSNode, u64Key(uint64(i)), val)
	}
}

// BenchmarkDiskRandomPointRead gets random keys from a 10_000 key snapshot.
func BenchmarkDiskRandomPointRead(b *testing.B) {
	const n = 10000
	eng := openBench(b)
	loadSequential(b, eng, n)
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tx.Rollback() })
	rng := rand.New(rand.NewSource(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tx.Get(storage.KSNode, u64Key(uint64(rng.Intn(n)))); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDiskRangeScan walks 10_000 keys from the start of the keyspace.
func BenchmarkDiskRangeScan(b *testing.B) {
	const n = 10000
	eng := openBench(b)
	loadSequential(b, eng, n)
	tx, err := eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tx.Rollback() })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cur, err := tx.Cursor(storage.KSNode)
		if err != nil {
			b.Fatal(err)
		}
		count := 0
		if cur.Seek(nil) {
			for {
				count++
				if !cur.Next() {
					break
				}
			}
		}
		if err := cur.Close(); err != nil {
			b.Fatal(err)
		}
		if count != n {
			b.Fatalf("scanned %d", count)
		}
	}
}

// adjKey matches graphstore outgoing adjacency: from, type, to, edge.
func adjKey(from uint64, typeID uint32, to, edge uint64) []byte {
	buf := make([]byte, 28)
	binary.BigEndian.PutUint64(buf[0:8], from)
	binary.BigEndian.PutUint32(buf[8:12], typeID)
	binary.BigEndian.PutUint64(buf[12:20], to)
	binary.BigEndian.PutUint64(buf[20:28], edge)
	return buf
}

// BenchmarkDiskAdjacencyFanout seeks one node's outgoing prefix and walks
// its 64 edges. Other nodes' keys sit on both sides of that prefix.
func BenchmarkDiskAdjacencyFanout(b *testing.B) {
	const fan = 64
	eng := openBench(b)
	tx, err := eng.Begin(storage.TxOptions{})
	if err != nil {
		b.Fatal(err)
	}
	var edge uint64 = 1
	for from := uint64(1); from <= 32; from++ {
		for n := 0; n < fan; n++ {
			key := adjKey(from, 1, uint64(n+1), edge)
			edge++
			if err := tx.Put(storage.KSOut, key, nil); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	tx, err = eng.Begin(storage.TxOptions{ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tx.Rollback() })
	prefix := adjKey(7, 0, 0, 0)[:8]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cur, err := tx.Cursor(storage.KSOut)
		if err != nil {
			b.Fatal(err)
		}
		count := 0
		if cur.Seek(prefix) {
			for bytes.HasPrefix(cur.Key(), prefix) {
				count++
				if !cur.Next() {
					break
				}
			}
		}
		if err := cur.Close(); err != nil {
			b.Fatal(err)
		}
		if count != fan {
			b.Fatalf("fan-out %d", count)
		}
	}
}

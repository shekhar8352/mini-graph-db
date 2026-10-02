# Benchmark baselines

Local numbers for the disk engine (`OpenEngine`). They are a record of one machine, not a CI gate. Re-run before a release and replace this table when the comparison is intentional.

- Date: 2026-10-02
- Go: go1.25.0 darwin/arm64
- CPU: Apple M4
- Command: `go test -run='^$' -bench='BenchmarkDisk' -benchmem -benchtime=1s -count=1 ./test/bench/`
- Checkpointer disabled for the run. `Close` still checkpoints.

| Benchmark | What one op does | ns/op | B/op | allocs/op |
|-----------|------------------|------:|-----:|----------:|
| `BenchmarkDiskSequentialInsert` | One key, one commit, one WAL sync | 10581769 | 143362 | 248 |
| `BenchmarkDiskRandomPointRead` | `Get` of a random key in a 10,000 key snapshot | 7278 | 20289 | 494 |
| `BenchmarkDiskRangeScan` | One forward scan of those 10,000 keys | 64081935 | 183930384 | 4486082 |
| `BenchmarkDiskAdjacencyFanout` | Seek one node's 8-byte prefix and walk its 64 outgoing edges (28-byte keys, 32 nodes × 64 edges loaded) | 161153 | 716277 | 7722 |

Sequential insert is dominated by `fsync`. The range scan is about 6.4 µs per key at this size. Adjacency keys match `graphstore` outgoing keys: `from uint64`, `type uint32`, `to uint64`, `edge uint64`, big-endian.

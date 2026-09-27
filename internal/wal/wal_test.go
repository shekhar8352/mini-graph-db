package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

func openTest(t *testing.T, opt Options) (*Log, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, dir
}

func readAll(t *testing.T, l *Log) []Record {
	t.Helper()
	r, err := l.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var out []Record
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
}

func equalRec(a, b Record) bool {
	return a.LSN == b.LSN && a.TxnID == b.TxnID && a.Type == b.Type && bytes.Equal(a.Payload, b.Payload)
}

func TestPayloadRoundTrip(t *testing.T) {
	page, err := EncodePageWrite(7, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	id, image, err := DecodePageWrite(page)
	if err != nil || id != 7 || !bytes.Equal(image, []byte{1, 2, 3, 4}) {
		t.Fatalf("page write %d %x %v", id, image, err)
	}
	ins := EncodeTreeInsert('N', []byte{}, []byte("v"))
	ks, key, val, err := DecodeTreeInsert(ins)
	if err != nil || ks != 'N' || len(key) != 0 || !bytes.Equal(val, []byte("v")) {
		t.Fatalf("insert %q %q %v", key, val, err)
	}
	del := EncodeTreeDelete('E', []byte{9})
	ks, key, err = DecodeTreeDelete(del)
	if err != nil || ks != 'E' || !bytes.Equal(key, []byte{9}) {
		t.Fatalf("delete %q %v", key, err)
	}
	cp := EncodeCheckpoint(42)
	nano, err := DecodeCheckpoint(cp)
	if err != nil || nano != 42 {
		t.Fatalf("checkpoint %d %v", nano, err)
	}
	if _, _, err := DecodePageWrite(nil); err == nil {
		t.Fatal("short page write")
	}
	if _, err := EncodePageWrite(1, nil); err == nil {
		t.Fatal("empty image")
	}
}

func TestAppendSyncReopenAndSeek(t *testing.T) {
	l, dir := openTest(t, Options{})
	payload := []byte("abc")
	lsn, err := l.Append(TypeTxnCommit, 3, payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'z'
	if l.Durable() != 1 {
		t.Fatalf("durable before sync %d", l.Durable())
	}
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	if l.Durable() != l.End() {
		t.Fatalf("durable %d end %d", l.Durable(), l.End())
	}
	second, err := l.Append(TypeTxnBegin, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	got := readAll(t, l2)
	if len(got) != 2 || !bytes.Equal(got[0].Payload, []byte("abc")) || got[0].LSN != lsn || got[0].TxnID != 3 {
		t.Fatalf("records %+v", got)
	}
	if got[1].LSN != second || got[1].Type != TypeTxnBegin || len(got[1].Payload) != 0 {
		t.Fatalf("second %+v", got[1])
	}
	r, err := l2.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.Seek(second); err != nil {
		t.Fatal(err)
	}
	rec, err := r.Next()
	if err != nil || rec.LSN != second {
		t.Fatalf("seek %v %+v", err, rec)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after last %v", err)
	}
	if err := r.Seek(0); err != nil {
		t.Fatal(err)
	}
	rec, err = r.Next()
	if err != nil || rec.LSN != lsn {
		t.Fatalf("seek start %v %+v", err, rec)
	}
	if err := r.Seek(l2.End()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("seek end %v", err)
	}
}

func TestAbandonDropsUnsyncedRecords(t *testing.T) {
	l, dir := openTest(t, Options{})
	if _, err := l.Append(TypeTxnAbort, 1, []byte("nope")); err != nil {
		t.Fatal(err)
	}
	l.abandon()
	l2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	if got := readAll(t, l2); len(got) != 0 {
		t.Fatalf("unsynced records survived %+v", got)
	}
	if _, err := l.Append(TypeTxnAbort, 1, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("append after abandon %v", err)
	}
}

func TestLogicalRecordsRoundTrip(t *testing.T) {
	l, dir := openTest(t, Options{})
	page, err := EncodePageWrite(4, []byte{9, 9})
	if err != nil {
		t.Fatal(err)
	}
	ins := EncodeTreeInsert('P', []byte("k"), []byte("v"))
	del := EncodeTreeDelete('L', nil)
	cp := EncodeCheckpoint(99)
	payloads := []struct {
		typ Type
		raw []byte
	}{
		{TypePageWrite, page},
		{TypeTreeInsert, ins},
		{TypeTreeDelete, del},
		{TypeCheckpoint, cp},
	}
	for _, p := range payloads {
		if _, err := l.Append(p.typ, 8, p.raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	got := readAll(t, l2)
	if len(got) != len(payloads) {
		t.Fatalf("len %d", len(got))
	}
	id, image, err := DecodePageWrite(got[0].Payload)
	if err != nil || id != 4 || !bytes.Equal(image, []byte{9, 9}) {
		t.Fatalf("page %d %x %v", id, image, err)
	}
	ks, key, val, err := DecodeTreeInsert(got[1].Payload)
	if err != nil || ks != 'P' || !bytes.Equal(key, []byte("k")) || !bytes.Equal(val, []byte("v")) {
		t.Fatalf("insert %v", err)
	}
	ks, key, err = DecodeTreeDelete(got[2].Payload)
	if err != nil || ks != 'L' || len(key) != 0 {
		t.Fatalf("delete %q %v", key, err)
	}
	nano, err := DecodeCheckpoint(got[3].Payload)
	if err != nil || nano != 99 {
		t.Fatalf("checkpoint %d %v", nano, err)
	}
}

func TestRejectsBadRecords(t *testing.T) {
	l, _ := openTest(t, Options{})
	if _, err := l.Append(99, 1, nil); err == nil {
		t.Fatal("unknown type")
	}
	big := make([]byte, MaxPayload+1)
	if _, err := l.Append(TypeTxnCommit, 1, big); err == nil {
		t.Fatal("huge payload")
	}
	if _, err := Open("", Options{}); err == nil {
		t.Fatal("empty dir")
	}
	if _, err := Open(t.TempDir(), Options{SegmentSize: 8}); err == nil {
		t.Fatal("tiny segment")
	}
}

func TestSegmentRotationAndArchiveRetry(t *testing.T) {
	body := []byte("rec")
	frame := recordHeader + len(body) + crcSize
	var mu sync.Mutex
	var calls int
	archiver := archiveFunc(func(info SegmentInfo) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if info.Number != 1 || info.First != 1 || info.End == info.First {
			t.Errorf("segment %+v", info)
		}
		if n == 1 {
			return gerr.New(gerr.Unavailable, "archive failed")
		}
		return nil
	})
	l, dir := openTest(t, Options{SegmentSize: int64(headerSize + frame), Archiver: archiver})
	if _, err := l.Append(TypeTxnCommit, 1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(TypeTxnCommit, 2, body); err != nil {
		t.Fatal(err)
	}
	if err := l.Sync(); err == nil {
		t.Fatal("expected archive error")
	}
	if l.Durable() == 1 {
		t.Fatal("first record was not sealed")
	}
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if calls != 2 {
		t.Fatalf("archive calls %d", calls)
	}
	mu.Unlock()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	got := readAll(t, l2)
	if len(got) != 2 || got[0].TxnID != 1 || got[1].TxnID != 2 || got[1].LSN != got[0].LSN+LSN(got[0].Size()) {
		t.Fatalf("records %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("segments %v", matches)
	}
}

func TestStrayFileAndSegmentGap(t *testing.T) {
	l, dir := openTest(t, Options{})
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(TypeTxnCommit, 1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, l2); len(got) != 1 {
		t.Fatalf("records %d", len(got))
	}
	_ = l2.Close()

	gap := t.TempDir()
	if err := os.WriteFile(filepath.Join(gap, segmentName(2)), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(gap, Options{}); err == nil {
		t.Fatal("gap accepted")
	}
}

func TestNewerVersionAndBadChecksum(t *testing.T) {
	l, dir := openTest(t, Options{})
	if _, err := l.Append(TypeTxnCommit, 1, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(TypeTxnCommit, 2, []byte("ghijkl")); err != nil {
		t.Fatal(err)
	}
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, segmentName(1))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint16(newer[4:6], FormatVersion+1)
	if err := os.WriteFile(path, newer, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir, Options{})
	var ge *gerr.Error
	if !errors.As(err, &ge) || ge.Code() != gerr.InvalidArgument {
		t.Fatalf("newer version %v", err)
	}

	broken := append([]byte(nil), raw...)
	broken[headerSize+recordHeader] ^= 0xff
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir, Options{})
	if !errors.As(err, &ge) || ge.Code() != gerr.Corruption {
		t.Fatalf("bad checksum %v", err)
	}
}

func TestTornTailEveryByte(t *testing.T) {
	l, dir := openTest(t, Options{})
	var written []Record
	for i := 0; i < 8; i++ {
		payload := []byte{byte(i), byte(i + 1), byte(i + 2), byte(i + 3)}
		lsn, err := l.Append(TypeTreeInsert, uint64(i+1), EncodeTreeInsert('N', payload, payload))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Sync(); err != nil {
			t.Fatal(err)
		}
		written = append(written, Record{LSN: lsn, TxnID: uint64(i + 1), Type: TypeTreeInsert, Payload: EncodeTreeInsert('N', payload, payload)})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, segmentName(1)))
	if err != nil {
		t.Fatal(err)
	}
	ends := make([]int, len(written))
	off := headerSize
	for i, rec := range written {
		off += rec.Size()
		ends[i] = off
	}
	if off != len(raw) {
		t.Fatalf("file %d computed %d", len(raw), off)
	}
	for cut := 0; cut <= len(raw); cut++ {
		gotDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(gotDir, segmentName(1)), raw[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		opened, err := Open(gotDir, Options{})
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		got := readAll(t, opened)
		want := 0
		for i, end := range ends {
			if end <= cut {
				want = i + 1
			}
		}
		if len(got) != want {
			_ = opened.Close()
			t.Fatalf("cut %d: got %d records, want %d", cut, len(got), want)
		}
		for i := 0; i < want; i++ {
			if !equalRec(got[i], written[i]) {
				_ = opened.Close()
				t.Fatalf("cut %d record %d %+v want %+v", cut, i, got[i], written[i])
			}
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKillWriterAtRandomOffsets(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2D))
	for trial := 0; trial < 100; trial++ {
		l, dir := openTest(t, Options{})
		var acked []Record
		for i := 0; i < 10; i++ {
			payload := []byte{byte(trial), byte(i)}
			lsn, err := l.Append(TypeTxnCommit, uint64(i+1), payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := l.Sync(); err != nil {
				t.Fatal(err)
			}
			acked = append(acked, Record{LSN: lsn, TxnID: uint64(i + 1), Type: TypeTxnCommit, Payload: append([]byte(nil), payload...)})
		}
		left := rng.Intn(90)
		l.writeHook = func(f *os.File, p []byte) (int, error) {
			if left <= 0 {
				return 0, gerr.New(gerr.Unavailable, "injected wal write failure")
			}
			n := len(p)
			var fail error
			if n > left {
				n = left
				fail = gerr.New(gerr.Unavailable, "injected wal write failure")
			}
			wrote, werr := f.Write(p[:n])
			left -= wrote
			if werr != nil {
				return wrote, werr
			}
			return wrote, fail
		}
		tail := bytes.Repeat([]byte{0x5a}, 48)
		lsn, err := l.Append(TypeTxnCommit, 99, tail)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Sync(); err == nil {
			acked = append(acked, Record{LSN: lsn, TxnID: 99, Type: TypeTxnCommit, Payload: append([]byte(nil), tail...)})
		}
		l.abandon()
		l2, err := Open(dir, Options{})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		got := readAll(t, l2)
		if len(got) < len(acked) {
			_ = l2.Close()
			t.Fatalf("trial %d: got %d acked %d", trial, len(got), len(acked))
		}
		for i := range acked {
			if !equalRec(got[i], acked[i]) {
				_ = l2.Close()
				t.Fatalf("trial %d record %d %+v want %+v", trial, i, got[i], acked[i])
			}
		}
		for i := len(acked); i < len(got); i++ {
			if got[i].Type != TypeTxnCommit || got[i].TxnID != 99 || !bytes.Equal(got[i].Payload, tail) {
				_ = l2.Close()
				t.Fatalf("trial %d extra %+v", trial, got[i])
			}
		}
		if err := l2.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGroupCommitOneFsync(t *testing.T) {
	l, _ := openTest(t, Options{})
	var syncs atomic.Int32
	l.syncHook = func(f *os.File) error {
		syncs.Add(1)
		return f.Sync()
	}
	l.delayFn = func(int) time.Duration {
		l.mu.Lock()
		for len(l.waiters) < 3 {
			l.cond.Wait()
		}
		l.mu.Unlock()
		return 0
	}
	start := make(chan struct{})
	errc := make(chan error, 4)
	var ready sync.WaitGroup
	ready.Add(4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			ready.Done()
			<-start
			if _, err := l.Append(TypeTxnCommit, uint64(i+1), []byte{byte(i)}); err != nil {
				errc <- err
				return
			}
			errc <- l.Sync()
		}(i)
	}
	ready.Wait()
	close(start)
	for i := 0; i < 4; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	if syncs.Load() != 1 {
		t.Fatalf("fsyncs %d", syncs.Load())
	}
	if got := readAll(t, l); len(got) != 4 {
		t.Fatalf("records %d", len(got))
	}
}

func TestLoneSyncDoesNotWait(t *testing.T) {
	l, _ := openTest(t, Options{GroupCommit: 200 * time.Millisecond})
	if _, err := l.Append(TypeTxnCommit, 1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := l.Sync(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 100*time.Millisecond {
		t.Fatalf("lone sync waited %s", time.Since(start))
	}
}

type archiveFunc func(SegmentInfo) error

func (f archiveFunc) Archive(info SegmentInfo) error { return f(info) }

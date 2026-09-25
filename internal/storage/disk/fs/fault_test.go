package fs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func testFile(t *testing.T) *Fault {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f")
	raw, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	ft := Wrap(FromOS(raw))
	t.Cleanup(func() { _ = ft.Close() })
	return ft
}

func TestFaultShortWriteAndFail(t *testing.T) {
	ft := testFile(t)
	ft.ShortWrite(2)
	n, err := ft.WriteAt([]byte("ABCDEF"), 0)
	if n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	buf := make([]byte, 6)
	n, err = ft.ReadAt(buf, 0)
	if n != 2 || (!errors.Is(err, io.EOF) && err != nil) {
		t.Fatalf("read n=%d err=%v %q", n, err, buf)
	}
	if string(buf[:2]) != "AB" {
		t.Fatalf("%q", buf)
	}

	ft.FailNextWrite(errors.New("full"))
	n, err = ft.WriteAt([]byte("ZZ"), 0)
	if n != 0 || err == nil || err.Error() != "full" {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = ft.WriteAt([]byte("Q"), 2)
	if n != 1 || err != nil {
		t.Fatalf("follow-up n=%d err=%v", n, err)
	}
}

func TestFaultReorderReverseAndDiscard(t *testing.T) {
	ft := testFile(t)
	ft.SetReorder(true)
	if _, err := ft.WriteAt([]byte("AAAA"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ft.WriteAt([]byte("BBBB"), 0); err != nil {
		t.Fatal(err)
	}
	if ft.Pending() != 2 {
		t.Fatalf("pending %d", ft.Pending())
	}
	buf := make([]byte, 4)
	if _, err := ft.ReadAt(buf, 0); err != nil || string(buf) != "BBBB" {
		t.Fatalf("overlay %q %v", buf, err)
	}
	ft.ReversePending()
	ft.FailNextSync(errors.New("no sync"))
	if err := ft.Sync(); err == nil || ft.Pending() != 2 {
		t.Fatalf("sync err pending %d", ft.Pending())
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := ft.ReadAt(buf, 0); err != nil || string(buf) != "AAAA" {
		t.Fatalf("reversed %q %v", buf, err)
	}

	ft.SetReorder(true)
	if _, err := ft.WriteAt([]byte("ZZZZ"), 0); err != nil {
		t.Fatal(err)
	}
	ft.Discard()
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := ft.ReadAt(buf, 0); err != nil || !bytes.Equal(buf, []byte("AAAA")) {
		t.Fatalf("discard kept %q %v", buf, err)
	}
}

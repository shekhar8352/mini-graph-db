// Package fs is the file interface the page store uses.
// Tests wrap a real file with Fault to fail, short-write, or reorder writes.
package fs

import (
	"io/fs"
	"os"
)

// File is the slice of *os.File the page store needs.
// ReadAt and WriteAt follow the os.File contract, including short results.
type File interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Truncate(size int64) error
	Close() error
	Stat() (fs.FileInfo, error)
}

// OpenFile opens name with the same flags *os.File accepts.
func OpenFile(name string, flag int, perm os.FileMode) (File, error) {
	f, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return FromOS(f), nil
}

// FromOS wraps an already opened file. The caller transfers ownership:
// Close closes f.
func FromOS(f *os.File) File {
	return &osFile{f: f}
}

type osFile struct {
	f *os.File
}

func (o *osFile) ReadAt(p []byte, off int64) (int, error) { return o.f.ReadAt(p, off) }
func (o *osFile) WriteAt(p []byte, off int64) (int, error) {
	return o.f.WriteAt(p, off)
}
func (o *osFile) Sync() error                { return o.f.Sync() }
func (o *osFile) Truncate(size int64) error  { return o.f.Truncate(size) }
func (o *osFile) Close() error               { return o.f.Close() }
func (o *osFile) Stat() (fs.FileInfo, error) { return o.f.Stat() }

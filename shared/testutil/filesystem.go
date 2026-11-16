package testutil

import (
	"errors"
	"io"
	"os"
)

// File wraps a real file so tests can fail read, write, sync and close independently.
// Failed close still releases the real handle, allowing restart tests to reopen.
// The handle is named rather than embedded so io.Copy cannot bypass injected
// Read/Write failures through promoted os.File.WriteTo/ReadFrom methods.
type File struct {
	File   *os.File
	Faults *Faults
}

func (f *File) Read(p []byte) (int, error) {
	if err := f.Faults.Hit("read"); err != nil {
		return 0, err
	}
	return f.File.Read(p)
}

func (f *File) Write(p []byte) (int, error) {
	if err := f.Faults.Hit("write"); err != nil {
		return 0, err
	}
	return f.File.Write(p)
}
func (f *File) Sync() error {
	if err := f.Faults.Hit("sync"); err != nil {
		return err
	}
	return f.File.Sync()
}
func (f *File) Close() error {
	injected := f.Faults.Hit("close")
	actual := f.File.Close()
	if injected != nil {
		return errors.Join(injected, actual)
	}
	return actual
}

// Filesystem leaves successful operations on a real temporary filesystem.
// Arm "open", "rename", "remove" or the file operations above.
type Filesystem struct{ Faults *Faults }

func (f Filesystem) OpenFile(path string, flags int, mode os.FileMode) (*File, error) {
	if err := f.Faults.Hit("open"); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, flags, mode)
	if err != nil {
		return nil, err
	}
	return &File{File: file, Faults: f.Faults}, nil
}
func (f Filesystem) Rename(from, to string) error {
	if err := f.Faults.Hit("rename"); err != nil {
		return err
	}
	return os.Rename(from, to)
}
func (f Filesystem) Remove(path string) error {
	if err := f.Faults.Hit("remove"); err != nil {
		return err
	}
	return os.Remove(path)
}

// ShortWriter injects a partial filesystem write, e.g. a full destination volume.
// Remaining is a byte budget; Err defaults to io.ErrShortWrite.
type ShortWriter struct {
	Writer    io.Writer
	Remaining int64
	Err       error
}

func (w *ShortWriter) Write(p []byte) (int, error) {
	err := w.Err
	if err == nil {
		err = io.ErrShortWrite
	}
	if len(p) == 0 {
		return 0, nil
	}
	if w.Remaining <= 0 {
		return 0, err
	}
	limited := int64(len(p)) > w.Remaining
	if limited {
		p = p[:int(w.Remaining)]
	}
	n, actual := w.Writer.Write(p)
	w.Remaining -= int64(n)
	if actual != nil {
		return n, actual
	}
	if limited {
		return n, err
	}
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

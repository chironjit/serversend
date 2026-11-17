// Package source opens upload sources without following symbolic links or
// directory redirections. Callers retain the returned handle while reading.
package source

import (
	"errors"
	"fmt"
	"os"
)

const LinkReason = "Symbolic link or directory redirection skipped; select its real target to upload its contents"

// SkipError is an intentional policy skip, never a permission or I/O failure.
type SkipError struct {
	Path   string
	Reason string
}

func (e *SkipError) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Reason) }

func Skipped(err error) (*SkipError, bool) {
	var skip *SkipError
	ok := errors.As(err, &skip)
	return skip, ok
}

// Open returns an ordinary file or directory. Every path component is opened
// without following links, so replacing a discovered path cannot redirect the
// read. Unsupported file types produce SkipError; actual I/O errors stay errors.
func Open(path string) (*os.File, error) { return open(path) }

func check(f *os.File, path string) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return &SkipError{path, "Unsupported special file skipped (" + info.Mode().Type().String() + ")"}
	}
	return nil
}

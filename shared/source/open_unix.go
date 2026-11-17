//go:build linux || darwin

package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func open(path string) (*os.File, error) { return openWithHook(path, nil) }

func openWithHook(path string, beforeOpen func(string)) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, name := range parts {
		if name == "" {
			name = "."
		}
		var discovered unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), name, &discovered, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			parent.Close()
			return nil, &os.PathError{Op: "inspect source", Path: path, Err: err}
		}
		switch discovered.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			parent.Close()
			return nil, &SkipError{path, LinkReason}
		case unix.S_IFDIR, unix.S_IFREG:
		default:
			parent.Close()
			return nil, &SkipError{path, "Unsupported special file skipped"}
		}
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		if beforeOpen != nil {
			beforeOpen(name)
		}
		fd, openErr := unix.Openat(int(parent.Fd()), name, flags, 0)
		if openErr != nil {
			// ELOOP is the kernel's atomic no-follow refusal. ENOTDIR may
			// identify a directory link; inspect relative to the held parent
			// to distinguish it from an ordinary non-directory ancestor.
			link := errors.Is(openErr, syscall.ELOOP)
			if errors.Is(openErr, syscall.ENOTDIR) {
				link = isLinkAt(parent, name)
			}
			parent.Close()
			if link {
				return nil, &SkipError{path, LinkReason}
			}
			return nil, &os.PathError{Op: "open source", Path: path, Err: openErr}
		}
		f := os.NewFile(uintptr(fd), path)
		parent.Close()
		if err := check(f, path); err != nil {
			f.Close()
			return nil, err
		}
		parent = f
	}
	return parent, nil
}

// This diagnostic is deliberately outside the safety boundary: openat has
// already refused the source, and this only chooses the displayed reason.
func isLinkAt(parent *os.File, name string) bool {
	var info unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW)
	return err == nil && info.Mode&unix.S_IFMT == unix.S_IFLNK
}

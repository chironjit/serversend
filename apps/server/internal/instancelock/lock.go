// Package instancelock prevents cooperating server processes from sharing a
// deployment directory. Locks require local filesystems with working native
// file locks and trusted directories; they are not distributed leases.
package instancelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const Filename = ".serversend.lock"

// Locks holds native locks until Close or process exit. Lock files remain on
// disk: unlinking them would let another process lock a different inode.
type Locks struct {
	mu    sync.Mutex
	files []*os.File
}

// Acquire locks every directory without waiting. If any acquisition fails,
// earlier locks are released. Paths must be existing trusted absolute paths.
func Acquire(directories ...string) (*Locks, error) {
	locks := &Locks{}
	for _, directory := range directories {
		if !filepath.IsAbs(directory) {
			locks.Close()
			return nil, errors.New("instance lock requires absolute directory paths")
		}
		file, err := openLock(directory)
		if err != nil {
			locks.Close()
			return nil, fmt.Errorf("lock deployment directory %s: %w", directory, err)
		}
		locks.files = append(locks.files, file)
	}
	return locks, nil
}

func openLock(directory string) (*os.File, error) {
	path := filepath.Join(directory, Filename)
	before, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, errors.New("instance lock must be a regular file, never a link")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		file.Close()
		return nil, errors.New("instance lock file changed or is not regular")
	}
	if err := lock(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("another server may be using this directory, or filesystem locking is unavailable: %w", err)
	}
	return file, nil
}

// Close releases all locks. It is safe to call repeatedly after serving stops.
func (l *Locks) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var failures []error
	for _, file := range l.files {
		failures = append(failures, file.Close())
	}
	l.files = nil
	return errors.Join(failures...)
}

package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Acquire before opening/recovering SQLite: Wails single-instance negotiation
// happens after service construction, so a second launch must not reset the
// first process's active tasks. Never unlink the lock inode.
func lockQueue(dir string) (*os.File, error) {
	path := filepath.Join(dir, ".queue.lock")
	before, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, errors.New("queue lock must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	current, e := os.Lstat(path)
	if err != nil || e != nil || !opened.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		file.Close()
		return nil, errors.New("queue lock changed while opening")
	}
	if err := nativeQueueLock(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("another desktop process is using this queue, or local file locking is unavailable: %w", err)
	}
	return file, nil
}

//go:build linux || darwin

package storage

import "golang.org/x/sys/unix"

func (s *Store) Available() (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(s.objects, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

//go:build linux || darwin

package storage

import (
	"fmt"
	"github.com/chironjit/serversend/shared/api"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func (s *Store) Available() (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(s.dir, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func allocated(info os.FileInfo, _ *os.Root, _ string) (int64, error) {
	return info.Sys().(*syscall.Stat_t).Blocks * 512, nil
}
func capacity(path string) (api.FilesystemCapacity, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return api.FilesystemCapacity{}, err
	}
	return api.FilesystemCapacity{ID: fmt.Sprintf("%v:%d", st.Fsid, st.Type), AvailableBytes: int64(st.Bavail) * int64(st.Bsize), TotalBytes: int64(st.Blocks) * int64(st.Bsize)}, nil
}

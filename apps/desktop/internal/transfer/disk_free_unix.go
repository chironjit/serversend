//go:build linux || darwin

package transfer

import "syscall"

// freeDiskBytes reports bytes available to this user on dir's filesystem.
func freeDiskBytes(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

//go:build linux || darwin

package transfer

import (
	"os"
	"syscall"
)

func nativeQueueLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

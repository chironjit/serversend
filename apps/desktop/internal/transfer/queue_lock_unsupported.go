//go:build !linux && !darwin && !windows

package transfer

import (
	"errors"
	"os"
)

func nativeQueueLock(*os.File) error {
	return errors.New("native desktop queue locking is unavailable on this platform")
}

//go:build !linux && !darwin && !windows

package instancelock

import (
	"errors"
	"os"
)

func lock(*os.File) error {
	return errors.New("native deployment locking is unsupported on this platform")
}

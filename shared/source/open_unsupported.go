//go:build !linux && !darwin && !windows

package source

import (
	"errors"
	"os"
)

func open(path string) (*os.File, error) {
	return nil, errors.New("safe source opening is unavailable on this platform")
}

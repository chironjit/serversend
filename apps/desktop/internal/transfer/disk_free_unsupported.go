//go:build !linux && !darwin && !windows

package transfer

// freeDiskBytes is unknown here; staging relies on the budget and ENOSPC.
func freeDiskBytes(string) (int64, bool) { return 0, false }

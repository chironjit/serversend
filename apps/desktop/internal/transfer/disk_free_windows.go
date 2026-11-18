package transfer

import "golang.org/x/sys/windows"

// freeDiskBytes reports bytes available to this user on dir's volume.
func freeDiskBytes(dir string) (int64, bool) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var free uint64
	if windows.GetDiskFreeSpaceEx(path, &free, nil, nil) != nil {
		return 0, false
	}
	return int64(free), true
}

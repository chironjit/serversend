package storage

import "golang.org/x/sys/windows"

func (s *Store) Available() (int64, error) {
	path, err := windows.UTF16PtrFromString(s.objects)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err = windows.GetDiskFreeSpaceEx(path, &free, nil, nil); err != nil {
		return 0, err
	}
	return int64(free), nil
}

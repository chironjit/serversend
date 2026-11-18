package storage

import (
	"github.com/chironjit/serversend/shared/api"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func (s *Store) Available() (int64, error) {
	path, err := windows.UTF16PtrFromString(s.dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err = windows.GetDiskFreeSpaceEx(path, &free, nil, nil); err != nil {
		return 0, err
	}
	return int64(free), nil
}

func allocated(_ os.FileInfo, root *os.Root, name string) (int64, error) {
	f, err := root.Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var info struct {
		AllocationSize int64
		EndOfFile      int64
		NumberOfLinks  uint32
		DeletePending  byte
		Directory      byte
	}
	err = windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileStandardInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info.AllocationSize, err
}
func capacity(path string) (api.FilesystemCapacity, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return api.FilesystemCapacity{}, err
	}
	var mount, volume [1024]uint16
	if err = windows.GetVolumePathName(p, &mount[0], 1024); err != nil {
		return api.FilesystemCapacity{}, err
	}
	if err = windows.GetVolumeNameForVolumeMountPoint(&mount[0], &volume[0], 1024); err != nil {
		return api.FilesystemCapacity{}, err
	}
	var free, total uint64
	err = windows.GetDiskFreeSpaceEx(p, &free, &total, nil)
	return api.FilesystemCapacity{ID: windows.UTF16ToString(volume[:]), AvailableBytes: int64(free), TotalBytes: int64(total)}, err
}

//go:build windows

package source

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func open(path string) (*os.File, error) { return openWithHook(path, nil) }

func openWithHook(path string, beforeOpen func(string)) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)
	root := volume + `\`
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open source", Path: path, Err: err}
	}
	parent := os.NewFile(uintptr(handle), root)
	if err := checkWindows(parent, path); err != nil {
		parent.Close()
		return nil, err
	}
	for _, name := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(abs, volume), `\`), `\`) {
		if name == "" {
			continue
		}
		component, err := windows.NewNTUnicodeString(name)
		if err != nil {
			parent.Close()
			return nil, err
		}
		attrs := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: windows.Handle(parent.Fd()), ObjectName: component, Attributes: windows.OBJ_CASE_INSENSITIVE}
		var next windows.Handle
		var status windows.IO_STATUS_BLOCK
		if beforeOpen != nil {
			beforeOpen(name)
		}
		// Each lookup is relative to an already-held directory handle. Opening
		// the reparse point itself prevents final-component redirection, even
		// when a regular entry is replaced between discovery and this call.
		err = windows.NtCreateFile(&next, windows.FILE_GENERIC_READ, &attrs, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
		parent.Close()
		if err != nil {
			return nil, &os.PathError{Op: "open source", Path: path, Err: err}
		}
		parent = os.NewFile(uintptr(next), path)
		if err := checkWindows(parent, path); err != nil {
			parent.Close()
			return nil, err
		}
	}
	return parent, nil
}

func reparseReason(attributes, tag uint32) string {
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return ""
	}
	// Microsoft defines the name-surrogate bit independently of specific
	// symlink/mount-point tags. This includes junctions and future redirections.
	if tag&0x20000000 != 0 {
		return LinkReason
	}
	if tag&0xFFFF0FFF == 0x9000001A {
		return "Cloud placeholder skipped; make a local ordinary copy before uploading"
	}
	return "Unsupported reparse point skipped; make a local ordinary copy before uploading"
}

func checkWindows(f *os.File, path string) error {
	kind, err := windows.GetFileType(windows.Handle(f.Fd()))
	if err != nil {
		return &os.PathError{Op: "inspect source", Path: path, Err: err}
	}
	if kind != windows.FILE_TYPE_DISK {
		return &SkipError{path, "Unsupported special file skipped"}
	}
	var tag struct{ Attributes, Tag uint32 }
	err = windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag)))
	if err != nil {
		return &os.PathError{Op: "inspect source", Path: path, Err: err}
	}
	if reason := reparseReason(tag.Attributes, tag.Tag); reason != "" {
		return &SkipError{path, reason}
	}
	return check(f, path)
}

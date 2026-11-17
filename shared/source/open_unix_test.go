//go:build linux || darwin

package source

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSpecialFilesArePolicySkipsWithoutBlocking(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if f != nil {
		f.Close()
		t.Fatal("opened fifo")
	}
	if skip, ok := Skipped(err); !ok || skip.Reason == LinkReason {
		t.Fatalf("special file classification: %v", err)
	}
}

func TestReplacementBetweenDiscoveryAndOpenCannotReadLinkTarget(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "ancestor"}[ancestor], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			writeSource(t, filepath.Join(outside, "data"), "must never read")
			source := filepath.Join(root, "source")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(source, "data")
			writeSource(t, path, "eligible original")
			name, target, replace := "data", filepath.Join(outside, "data"), path
			if ancestor {
				name, target, replace = "source", outside, source
			}
			f, err := openWithHook(path, func(component string) {
				if component != name {
					return
				}
				if err := os.Rename(replace, replace+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, replace); err != nil {
					t.Fatal(err)
				}
			})
			if f != nil {
				f.Close()
				t.Fatal("opened replacement link")
			}
			if _, ok := Skipped(err); !ok {
				t.Fatalf("replacement was not a link skip: %v", err)
			}
		})
	}
}

func TestHeldHandleReadsOriginalAfterPathReplacement(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file")
	writeSource(t, path, "original")
	target := filepath.Join(root, "target")
	writeSource(t, target, "outside contents")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "original" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestPermissionFailureIsNotASkip(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses source permissions; unprivileged native gate remains")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unreadable")
	writeSource(t, path, "secret")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	f, err := Open(path)
	if f != nil {
		f.Close()
		t.Fatal("opened unreadable fixture")
	}
	if err == nil {
		t.Fatal("permission failure succeeded")
	}
	if _, ok := Skipped(err); ok {
		t.Fatal("permission error hidden as policy skip")
	}
}

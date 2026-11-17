//go:build windows

package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReparseTagsHaveDistinctPolicyReasons(t *testing.T) {
	for _, test := range []struct {
		name       string
		attrs, tag uint32
		want       string
	}{
		{"ordinary", 0, 0, ""},
		{"symbolic link", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0xA000000C, LinkReason},
		{"junction", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0xA0000003, LinkReason},
		{"other name surrogate", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0xA0000022, LinkReason},
		{"cloud", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x9000001A, "Cloud placeholder"},
		{"cloud variant", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x9000F01A, "Cloud placeholder"},
		{"unsupported non-surrogate", windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x80000013, "Unsupported reparse point"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := reparseReason(test.attrs, test.tag)
			if test.want == "" {
				if got != "" {
					t.Fatalf("ordinary marked skipped: %s", got)
				}
			} else if !strings.HasPrefix(got, test.want) {
				t.Fatalf("reason=%q want prefix %q", got, test.want)
			}
		})
	}
}

func TestWindowsPipeIsAnUnsupportedSpecialFile(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if skip, ok := Skipped(checkWindows(r, "pipe fixture")); !ok || skip.Reason == LinkReason {
		t.Fatalf("pipe was not a distinct special-file skip: %v", skip)
	}
}

func TestNativeJunctionAndItsDescendantsAreSkipped(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	writeSource(t, filepath.Join(target, "file"), "outside junction data")
	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Skipf("native junction fixture unavailable; Windows filesystem/privilege gate remains: %v %s", err, output)
	}
	for _, path := range []string{junction, filepath.Join(junction, "file")} {
		f, err := Open(path)
		if f != nil {
			f.Close()
			t.Fatal("opened junction target")
		}
		if skip, ok := Skipped(err); !ok || skip.Reason != LinkReason {
			t.Fatalf("junction classification: %v", err)
		}
	}
}

func TestWindowsReplacementBeforeRelativeOpenIsSkipped(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[ancestor], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source")
			outside := filepath.Join(root, "outside")
			for _, path := range []string{source, outside} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(source, "file")
			writeSource(t, path, "original")
			writeSource(t, filepath.Join(outside, "file"), "must not read")
			component, replace, target := "file", path, filepath.Join(outside, "file")
			if ancestor {
				component, replace, target = "source", source, outside
			}
			f, err := openWithHook(path, func(name string) {
				if name != component {
					return
				}
				if err := os.Rename(replace, replace+"-old"); err != nil {
					t.Fatal(err)
				}
				makeLink(t, target, replace)
			})
			if f != nil {
				f.Close()
				t.Fatal("opened replacement redirection")
			}
			if _, ok := Skipped(err); !ok {
				t.Fatalf("replacement=%v", err)
			}
		})
	}
}

func TestWindowsACLPermissionFailureIsNotASkip(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := "*" + user.User.Sid.String()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "denied")
	writeSource(t, path, "secret")
	if output, err := exec.Command("icacls", path, "/deny", sid+":(R)").CombinedOutput(); err != nil {
		t.Skipf("native ACL fixture unavailable; Windows permission gate remains: %v %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("icacls", path, "/remove:d", sid).CombinedOutput(); err != nil {
			t.Errorf("restore source ACL: %v %s", err, output)
		}
	})
	f, err := Open(path)
	if f != nil {
		f.Close()
		t.Fatal("opened ACL-denied source")
	}
	if err == nil {
		t.Fatal("ACL denial succeeded")
	}
	if _, ok := Skipped(err); ok {
		t.Fatal("ACL denial hidden as policy skip")
	}
}

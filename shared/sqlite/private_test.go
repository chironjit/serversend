package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenPrivatePaths(t *testing.T) {
	for _, scenario := range []string{"normal", "missing", "relative", "directory", "symlink", "public file", "public directory"} {
		t.Run(scenario, func(t *testing.T) {
			if runtime.GOOS == "windows" && (scenario == "public file" || scenario == "public directory" || scenario == "symlink") {
				t.Skip("ACL/symlink privileges require native platform qualification")
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(dir, "private.db")
			switch scenario {
			case "missing":
				filename = filepath.Join(dir, "missing", "private.db")
			case "relative":
				filename = "relative.db"
			case "directory":
				filename = dir
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "target"), filename); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.WriteFile(filename, nil, 0644); err != nil {
					t.Fatal(err)
				}
			case "public directory":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			db, err := OpenPrivate(context.Background(), filename)
			if scenario == "normal" {
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			} else if err == nil || db != nil {
				if db != nil {
					db.Close()
				}
				t.Fatal("unsafe path accepted")
			}
		})
	}
}

func TestOpenReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL qualification requires native setup")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	probe, err := os.CreateTemp(dir, "write-probe")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("current user bypasses directory permissions")
	}
	filename := filepath.Join(dir, "blocked.db")
	if db, err := OpenPrivate(context.Background(), filename); err == nil || db != nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("read-only parent accepted")
	}
}

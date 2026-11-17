package source

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeSource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func makeLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("native symlink fixture unavailable (platform gate remains): %v", err)
	}
}

func TestOpenRejectsLinksIncludingAncestorsAndCycles(t *testing.T) {
	root := t.TempDir()
	// macOS often places temporary directories below /var, itself a symlink.
	// Fixtures use the real ancestor because ancestor links are intentionally
	// refused, regardless of whether they are system aliases.
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	writeSource(t, filepath.Join(real, "file"), "private target bytes")
	makeLink(t, filepath.Join(real, "file"), filepath.Join(root, "file-link"))
	makeLink(t, real, filepath.Join(root, "dir-link"))
	makeLink(t, filepath.Join(root, "missing"), filepath.Join(root, "broken"))
	makeLink(t, root, filepath.Join(real, "cycle"))
	for _, path := range []string{"file-link", "dir-link", "broken", "dir-link/file", "real/cycle/real/file"} {
		t.Run(path, func(t *testing.T) {
			f, err := Open(filepath.Join(root, path))
			if f != nil {
				f.Close()
				t.Fatal("opened linked target")
			}
			if _, ok := Skipped(err); !ok {
				t.Fatalf("expected policy skip: %v", err)
			}
		})
	}
	if _, err := Open(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing source succeeded")
	} else if _, ok := Skipped(err); ok {
		t.Fatal("missing source was hidden as a skip")
	}
}

func TestOrdinaryFilesDirectoriesAndHardLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file")
	writeSource(t, path, "ordinary bytes")
	alias := filepath.Join(root, "hard-link")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard-link fixture unavailable: %v", err)
	}
	for _, name := range []string{path, alias} {
		f, err := Open(name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(data) != "ordinary bytes" {
			t.Fatalf("%q %v", data, err)
		}
	}
	f, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if entries, err := f.ReadDir(-1); err != nil || len(entries) != 2 {
		t.Fatalf("directory entries=%v err=%v", entries, err)
	}
}

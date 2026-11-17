package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

func digestOf(b []byte) protocol.Digest {
	sum := sha256.Sum256(b)
	return protocol.Digest(hex.EncodeToString(sum[:]))
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Replacing a file retains the previous version. Replaying the journal after
// a crash at any point reaches the same final state.
func TestApplyReplaysReplacementFromAnyPoint(t *testing.T) {
	ops := []Op{
		{Kind: "move", Source: "alice/Docs/report.txt", Target: ".serversend/versions/a/f/old"},
		{Kind: "move", Source: ".serversend/uploads/new", Target: "alice/Docs/report.txt"},
	}
	for applied := 0; applied <= len(ops); applied++ {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "alice/Docs/report.txt"), "old")
		write(t, filepath.Join(dir, ".serversend/uploads/new"), "new")
		if err := s.Apply(ops[:applied]); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := s.Apply(ops); err != nil {
				t.Fatal(err)
			}
		}
		if read(t, filepath.Join(dir, "alice/Docs/report.txt")) != "new" || read(t, filepath.Join(dir, ".serversend/versions/a/f/old")) != "old" {
			t.Fatalf("crash after %d ops: wrong final state", applied)
		}
		s.Close()
	}
}

func TestApplyKeepsHandPlacedFileAside(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write(t, filepath.Join(dir, "alice/note.txt"), "by hand")
	write(t, filepath.Join(dir, ".serversend/tmp/x"), "uploaded")
	if err := s.Apply([]Op{{Kind: "move", Source: ".serversend/tmp/x", Target: "alice/note.txt"}}); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "alice/note.txt")) != "uploaded" {
		t.Fatal("upload did not land")
	}
	kept, err := os.ReadDir(filepath.Join(dir, ".serversend/untracked"))
	if err != nil || len(kept) != 1 || read(t, filepath.Join(dir, ".serversend/untracked", kept[0].Name())) != "by hand" {
		t.Fatalf("hand-placed file lost: %v", err)
	}
}

func TestApplyNeverFollowsLinksOutOfTheDataDirectory(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := os.Symlink(outside, filepath.Join(dir, "alice")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	write(t, filepath.Join(dir, ".serversend/tmp/x"), "data")
	if err := s.Apply([]Op{{Kind: "move", Source: ".serversend/tmp/x", Target: "alice/escaped"}}); err == nil {
		t.Fatal("move through a link accepted")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote outside the data directory")
	}
}

type failedFile struct {
	*os.File
	failure string
}

func (f failedFile) Write(p []byte) (int, error) {
	switch f.failure {
	case "short":
		return len(p) - 1, nil
	case "full":
		return 0, syscall.ENOSPC
	}
	return f.File.Write(p)
}

func TestFailedStagingLeavesNothing(t *testing.T) {
	for _, failure := range []string{"short", "full", "truncated", "digest"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.createTemporary = func(name string) (objectFile, error) {
				f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
				return failedFile{f, failure}, err
			}
			body := []byte("data")
			var reader io.Reader = bytes.NewReader(body)
			if failure == "truncated" {
				reader = bytes.NewReader(body[:2])
			}
			if failure == "digest" {
				reader = bytes.NewReader([]byte("else"))
			}
			if _, err := s.Stage(reader, 4, digestOf(body)); err == nil {
				t.Fatal("failure ignored")
			}
			if files, err := os.ReadDir(filepath.Join(dir, ".serversend/tmp")); err != nil || len(files) != 0 {
				t.Fatalf("temporary files retained: %v %v", files, err)
			}
		})
	}
}

func TestChunksAreWrittenInPlaceAndVerified(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := protocol.ID("cccccccccccccccccccccccccccccccc")
	if err := s.WriteChunk(id, 0, 2, digestOf([]byte("ab")), bytes.NewReader([]byte("ab"))); !errors.Is(err, ErrGone) {
		t.Fatalf("write without upload: %v", err)
	}
	if err := s.CreateUpload(id); err != nil {
		t.Fatal(err)
	}
	// The final chunk may arrive first; a corrupt chunk is overwritten by its retry.
	if err := s.WriteChunk(id, 2, 1, digestOf([]byte("c")), bytes.NewReader([]byte("c"))); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChunk(id, 0, 2, digestOf([]byte("ab")), bytes.NewReader([]byte("xy"))); !errors.Is(err, ErrMismatch) {
		t.Fatalf("corrupt chunk: %v", err)
	}
	if err := s.WriteChunk(id, 0, 2, digestOf([]byte("ab")), bytes.NewReader([]byte("ab"))); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishUpload(id, 3); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, ".serversend/uploads", string(id))); got != "abc" {
		t.Fatalf("assembled %q", got)
	}
}

func TestOpenRefusesLegacyObjectLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrLegacyLayout) {
		t.Fatalf("legacy layout accepted: %v", err)
	}
}

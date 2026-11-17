package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

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
	case "readonly":
		return 0, syscall.EROFS
	}
	return f.File.Write(p)
}
func (f failedFile) Sync() error {
	if f.failure == "flush" {
		return syscall.EIO
	}
	return f.File.Sync()
}

func TestFailedWriteNeverPublishes(t *testing.T) {
	for _, failure := range []string{"short", "full", "readonly", "flush", "truncated", "digest"} {
		t.Run(failure, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s.createTemporary = func(dir, pattern string) (objectFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				return failedFile{f, failure}, nil
			}
			body := []byte("data")
			sum := sha256.Sum256(body)
			digest := protocol.Digest(hex.EncodeToString(sum[:]))
			var reader io.Reader = bytes.NewReader(body)
			if failure == "truncated" {
				reader = bytes.NewReader(body[:2])
			}
			if failure == "digest" {
				reader = bytes.NewReader([]byte("else"))
			}
			id := protocol.ID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err = s.Put(id, reader, 4, digest); err == nil {
				t.Fatal("failure ignored")
			}
			if f, err := s.Open(id); err == nil {
				f.Close()
				t.Fatal("failed object published")
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			files, err := os.ReadDir(s.temporary)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files retained: %v %v", files, err)
			}
		})
	}
}

// Package storage keeps immutable chunk objects on the data volume. Object
// names are server-generated identifiers, never client input, so no request
// can address a path outside the store.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chironjit/serversend/shared/protocol"
)

var ErrMismatch = errors.New("object bytes do not match the declared length and digest")

type Store struct{ objects, temporary string }

// Open prepares private directories inside an existing data directory and
// discards temporary files left by an interrupted process. Temporary files
// live on the data filesystem so publication is an atomic rename.
func Open(dataDir string) (*Store, error) {
	s := &Store{objects: filepath.Join(dataDir, "objects"), temporary: filepath.Join(dataDir, "incoming")}
	if err := os.RemoveAll(s.temporary); err != nil {
		return nil, fmt.Errorf("clear interrupted object writes: %w", err)
	}
	for _, dir := range []string{s.objects, s.temporary} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("prepare object storage: %w", err)
		}
	}
	return s, nil
}

func (s *Store) path(id protocol.ID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.objects, string(id[:2]), string(id)), nil
}

// Put streams at most length bytes to a temporary file, verifies the exact
// length and SHA-256, flushes file and directory, and only then publishes the
// object. A mismatch or failure leaves nothing behind.
func (s *Store) Put(id protocol.ID, body io.Reader, length int64, digest protocol.Digest) (err error) {
	final, err := s.path(id)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.temporary, "object-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, length+1))
	if err != nil {
		return err
	}
	if written != length || hex.EncodeToString(hash.Sum(nil)) != string(digest) {
		return ErrMismatch
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), final); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(final))
}

func (s *Store) Open(id protocol.ID) (*os.File, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *Store) Remove(id protocol.ID) {
	if path, err := s.path(id); err == nil {
		os.Remove(path)
	}
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

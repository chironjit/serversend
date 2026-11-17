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

type objectFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}
type Store struct {
	objects, temporary string
	createTemporary    func(string, string) (objectFile, error)
}

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
	if err := syncDirectory(dataDir); err != nil {
		return nil, err
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
func (s *Store) Put(id protocol.ID, body io.Reader, length int64, digest protocol.Digest) error {
	staged, err := s.Stage(body, length, digest)
	if err != nil {
		return err
	}
	return s.Publish([]protocol.ID{id}, []*Staged{staged})
}

// Staged is verified object data that is not yet durable or visible.
type Staged struct{ file objectFile }

// Discard removes data that will not be published.
func (st *Staged) Discard() {
	st.file.Close()
	os.Remove(st.file.Name())
}

// Stage reads at most length+1 bytes and verifies the exact length and SHA-256.
func (s *Store) Stage(body io.Reader, length int64, digest protocol.Digest) (staged *Staged, err error) {
	create := s.createTemporary
	if create == nil {
		create = func(dir, pattern string) (objectFile, error) { return os.CreateTemp(dir, pattern) }
	}
	file, err := create(s.temporary, "object-*")
	if err != nil {
		return nil, err
	}
	staged = &Staged{file: file}
	defer func() {
		if err != nil {
			staged.Discard()
			staged = nil
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, length+1))
	if err != nil {
		return staged, err
	}
	if written != length || hex.EncodeToString(hash.Sum(nil)) != string(digest) {
		return staged, ErrMismatch
	}
	return staged, nil
}

// Publish flushes staged objects concurrently, renames them to ids, then
// flushes each affected directory once: object shards, object root and
// incoming. On failure nothing staged remains and published objects are
// removed; none is referenced yet.
func (s *Store) Publish(ids []protocol.ID, staged []*Staged) (err error) {
	finals := make([]string, len(ids))
	published := 0
	defer func() {
		if err != nil {
			for i := range staged {
				if i < published {
					os.Remove(finals[i])
				} else {
					staged[i].Discard()
				}
			}
		}
	}()
	if len(ids) != len(staged) {
		return errors.New("each staged object needs one ID")
	}
	for i, id := range ids {
		if finals[i], err = s.path(id); err != nil {
			return err
		}
	}
	// Concurrent flushes let the filesystem combine journal commits.
	failures := make(chan error, len(staged))
	limit := make(chan struct{}, 16)
	for _, st := range staged {
		limit <- struct{}{}
		go func() {
			defer func() { <-limit }()
			failures <- st.file.Sync()
		}()
	}
	for range staged {
		err = errors.Join(err, <-failures)
	}
	if err != nil {
		return err
	}
	shards := map[string]bool{}
	for i, st := range staged {
		if err = st.file.Close(); err != nil {
			return err
		}
		shard := filepath.Dir(finals[i])
		if !shards[shard] {
			if err = os.MkdirAll(shard, 0o700); err != nil {
				return err
			}
			shards[shard] = true
		}
		if err = os.Rename(st.file.Name(), finals[i]); err != nil {
			return err
		}
		published = i + 1
	}
	// Persist each shard entry in objects as well as the new file entries. Both
	// sides of every rename must be durable before SQLite acknowledges objects.
	for shard := range shards {
		if err = syncDirectory(shard); err != nil {
			return err
		}
	}
	if err = syncDirectory(s.objects); err != nil {
		return err
	}
	return syncDirectory(s.temporary)
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

func (s *Store) Delete(id protocol.ID) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		// A previous unlink may have succeeded before directory flush failed.
		// Persist it before forgetting the durable deletion intent.
		if _, statErr := os.Stat(filepath.Dir(path)); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

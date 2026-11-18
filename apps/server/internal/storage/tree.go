// Package storage keeps file contents on the data volume as a browsable tree:
// each user's current files appear under data/<username> with their real
// folder and file names. See package layout for the complete layout. Every
// path is resolved through os.Root, so no stored link can lead outside it.
package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/shared/protocol"
)

var (
	ErrMismatch = errors.New("bytes do not match the declared length and digest")
	ErrGone     = errors.New("upload file no longer exists")
	// ErrSizeChanged reports a stored file whose size differs from its upload.
	ErrSizeChanged = errors.New("size differs from the uploaded file")
	// ErrLegacyLayout refuses data written by the pre-release object store.
	ErrLegacyLayout = errors.New("the data directory uses the pre-release object layout; stop the server and run serversend -reset-files to remove all uploaded files (accounts are kept), or restore a matching backup")
)

type objectFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

type Store struct {
	dir             string
	root            *os.Root
	createTemporary func(name string) (objectFile, error)
}

// Open prepares the internal area of an existing data directory and discards
// unpublished batch files left by an interrupted process. Pending moves must
// be replayed before uploads are cleaned; see Apply and CleanUploads.
func Open(dataDir string) (*Store, error) {
	for _, legacy := range []string{"objects", "incoming"} {
		if _, err := os.Lstat(filepath.Join(dataDir, legacy)); err == nil {
			return nil, ErrLegacyLayout
		}
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dataDir, root: root}
	for _, dir := range []string{layout.Uploads, layout.Versions, layout.Trash, layout.Tmp} {
		if err := root.MkdirAll(dir, 0o700); err != nil {
			root.Close()
			return nil, fmt.Errorf("prepare file storage: %w", err)
		}
	}
	if err := s.syncDirs(map[string]bool{".": true, layout.Internal: true}); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.root.Close() }

func randomName() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

// ClearTmp removes unpublished batch files. Call it after Apply has replayed
// every pending move, because a pending move may still name a batch file.
func (s *Store) ClearTmp() error {
	if err := s.root.RemoveAll(layout.Tmp); err != nil {
		return err
	}
	if err := s.root.Mkdir(layout.Tmp, 0o700); err != nil {
		return err
	}
	return s.syncDirs(map[string]bool{layout.Internal: true})
}

// ClearTrash removes leftovers of interrupted removals once none is pending.
func (s *Store) ClearTrash() error {
	if err := s.root.RemoveAll(layout.Trash); err != nil {
		return err
	}
	if err := s.root.Mkdir(layout.Trash, 0o700); err != nil {
		return err
	}
	return s.syncDirs(map[string]bool{layout.Internal: true})
}

// CreateUpload makes the empty, durable file an upload's chunks are written into.
func (s *Store) CreateUpload(id protocol.ID) error {
	// An empty file has no contents to flush; its directory entry must be
	// durable before any chunk in it is acknowledged.
	f, err := s.root.OpenFile(layout.Upload(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return s.syncDirs(map[string]bool{layout.Uploads: true})
}

func (s *Store) RemoveUpload(id protocol.ID) {
	_ = s.root.Remove(layout.Upload(id))
}

// WriteChunk writes exactly length bytes at offset, verifies their SHA-256
// and flushes them. Bytes that fail verification stay unacknowledged and are
// overwritten by a retry. The caller ensures one writer per chunk.
func (s *Store) WriteChunk(id protocol.ID, offset, length int64, digest protocol.Digest, body io.Reader) error {
	f, err := s.root.OpenFile(layout.Upload(id), os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrGone
	}
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(io.NewOffsetWriter(f, offset), hash), io.LimitReader(body, length))
	if err != nil {
		return err
	}
	if n, _ := body.Read(make([]byte, 1)); n != 0 || written != length || hex.EncodeToString(hash.Sum(nil)) != string(digest) {
		return ErrMismatch
	}
	return f.Sync()
}

// FinishUpload fixes the upload file's exact length before publication.
func (s *Store) FinishUpload(id protocol.ID, size int64) error {
	f, err := s.root.OpenFile(layout.Upload(id), os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrGone
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == size {
		return err // the final chunk already ended the file
	}
	if err := f.Truncate(size); err != nil {
		return err
	}
	return f.Sync()
}

// Staged is a verified batch file that is not yet durable or visible.
type Staged struct {
	file objectFile
	Path string
}

func (st *Staged) Discard(s *Store) {
	st.file.Close()
	_ = s.root.Remove(st.Path)
}

// Stage reads exactly length bytes into the temporary area and verifies them.
// It consumes length bytes from body even when verification fails.
func (s *Store) Stage(body io.Reader, length int64, digest protocol.Digest, observe ...func(string)) (staged *Staged, err error) {
	name := layout.Tmp + "/" + randomName()
	create := s.createTemporary
	if create == nil {
		create = func(name string) (objectFile, error) {
			return s.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		}
	}
	file, err := create(name)
	if err != nil {
		return nil, err
	}
	staged = &Staged{file: file, Path: name}
	if len(observe) > 0 {
		observe[0](name)
	}
	defer func() {
		if err != nil {
			staged.Discard(s)
			staged = nil
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, length))
	if err != nil {
		return staged, err
	}
	if written != length || hex.EncodeToString(hash.Sum(nil)) != string(digest) {
		return staged, ErrMismatch
	}
	return staged, nil
}

// SyncStaged flushes staged files concurrently, letting the filesystem
// combine journal commits, then closes them. They are published by Apply.
func (s *Store) SyncStaged(staged []*Staged) error {
	failures := make(chan error, len(staged))
	limit := make(chan struct{}, 16)
	for _, st := range staged {
		limit <- struct{}{}
		go func() {
			defer func() { <-limit }()
			failures <- errors.Join(st.file.Sync(), st.file.Close())
		}()
	}
	var err error
	for range staged {
		err = errors.Join(err, <-failures)
	}
	return err
}

// Op is one step of a journaled filesystem change.
type Op struct {
	Kind   string // "move", "remove" or "mkdir"
	Source string // move only
	Target string
}

// Apply performs ops in order and flushes every affected directory. Each op
// is idempotent, so a sequence interrupted by a crash can be replayed from
// the start: a move whose source is gone or whose target exists is complete,
// and removal and folder creation repeat safely. Removals take whole trees,
// so callers first move them to a unique trash entry.
func (s *Store) Apply(ops []Op) error {
	dirty := map[string]bool{}
	for _, op := range ops {
		switch op.Kind {
		case "mkdir":
			if err := s.ensureDir(op.Target, dirty); err != nil {
				return err
			}
		case "remove":
			if err := s.root.RemoveAll(op.Target); err != nil {
				return err
			}
			dirty[path.Dir(op.Target)] = true
		case "move":
			if _, err := s.root.Lstat(op.Source); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			if _, err := s.root.Lstat(op.Target); err == nil {
				// Earlier ops have run, so an occupied target is either this
				// move already done (its source is retained content), or an
				// entry placed by hand where new content belongs: keep it aside.
				if !strings.HasPrefix(op.Source, layout.Uploads+"/") && !strings.HasPrefix(op.Source, layout.Tmp+"/") {
					continue
				}
				aside := layout.Untracked + "/" + randomName()
				if err := s.root.MkdirAll(layout.Untracked, 0o700); err != nil {
					return err
				}
				if err := s.root.Rename(op.Target, aside); err != nil {
					return err
				}
				dirty[layout.Untracked], dirty[layout.Internal] = true, true
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := s.ensureDir(path.Dir(op.Target), dirty); err != nil {
				return err
			}
			if err := s.root.Rename(op.Source, op.Target); err != nil {
				return err
			}
			dirty[path.Dir(op.Source)], dirty[path.Dir(op.Target)] = true, true
		default:
			return fmt.Errorf("unknown storage operation %q", op.Kind)
		}
	}
	return s.syncDirs(dirty)
}

// ensureDir creates dir and any missing parents, marking each directory
// that gained an entry for flushing: the deepest existing ancestor and every
// created directory. Existing directories are not flushed again.
func (s *Store) ensureDir(dir string, dirty map[string]bool) error {
	missing := []string{}
	for current := dir; current != "."; current = path.Dir(current) {
		if _, err := s.root.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
	}
	if len(missing) == 0 {
		return nil
	}
	if err := s.root.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dirty[path.Dir(missing[len(missing)-1])] = true
	for _, created := range missing {
		dirty[created] = true
	}
	return nil
}

func (s *Store) syncDirs(dirs map[string]bool) error {
	for dir := range dirs {
		f, err := s.root.Open(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed later in the same sequence
		}
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

// Open opens stored contents for reading.
func (s *Store) Open(name string) (*os.File, error) { return s.root.Open(name) }

// Occupied reports whether something exists at a visible path, so an
// unexpected entry is never silently replaced.
func (s *Store) Occupied(name string) (bool, error) {
	_, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Verify checks that acknowledged contents exist with their recorded size.
// A present regular file of another size returns ErrSizeChanged. It never
// deletes metadata to hide storage corruption.
func (s *Store) Verify(name string, size int64, exact bool) error {
	info, err := s.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("stored file %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("stored file %s is not a regular file", name)
	}
	if info.Size() < size || exact && info.Size() != size {
		return fmt.Errorf("stored file %s: %w", name, ErrSizeChanged)
	}
	return nil
}

// CleanUploads removes upload files that no open upload owns.
func (s *Store) CleanUploads(keep func(protocol.ID) (bool, error)) error {
	dir, err := s.root.Open(layout.Uploads)
	if err != nil {
		return err
	}
	defer dir.Close()
	removed := false
	for {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			id := protocol.ID(entry.Name())
			wanted := false
			if id.Validate() == nil && entry.Type().IsRegular() {
				if wanted, err = keep(id); err != nil {
					return err
				}
			}
			if !wanted {
				if err := s.root.RemoveAll(layout.Uploads + "/" + entry.Name()); err != nil {
					return err
				}
				removed = true
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if removed {
		return s.syncDirs(map[string]bool{layout.Uploads: true})
	}
	return nil
}

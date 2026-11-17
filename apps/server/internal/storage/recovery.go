package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chironjit/serversend/shared/protocol"
)

// Reconcile runs only before requests are accepted, under the deployment lock.
// It streams the tree and asks SQLite about each object, bounding memory use.
func (s *Store) Reconcile(ctx context.Context, referenced func(context.Context, protocol.ID) (bool, error)) error {
	root, err := os.Open(s.objects)
	if err != nil {
		return err
	}
	defer root.Close()
	return eachEntry(root, func(shard os.DirEntry) error {
		name := shard.Name()
		if !shard.IsDir() || len(name) != 2 || strings.Trim(name, "0123456789abcdef") != "" {
			return fmt.Errorf("unexpected object shard %q", name)
		}
		dir, err := os.Open(filepath.Join(s.objects, name))
		if err != nil {
			return err
		}
		defer dir.Close()
		return eachEntry(dir, func(entry os.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := protocol.ID(entry.Name())
			if !entry.Type().IsRegular() || id.Validate() != nil || string(id[:2]) != name {
				return fmt.Errorf("unexpected object entry %q", entry.Name())
			}
			keep, err := referenced(ctx, id)
			if err != nil {
				return err
			}
			if !keep {
				return s.Delete(id)
			}
			return nil
		})
	})
}

func eachEntry(dir *os.File, visit func(os.DirEntry) error) error {
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// VerifyExists refuses startup with missing/truncated acknowledged data. It
// never deletes metadata for a retained version to hide storage corruption.
func (s *Store) VerifyExists(id protocol.ID, size int64) error {
	f, err := s.Open(id)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("invalid stored object %s", id)
	}
	return nil
}

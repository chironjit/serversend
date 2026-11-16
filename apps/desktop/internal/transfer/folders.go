package transfer

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/protocol"
	"io"
	"os"
	"path/filepath"
)

func (m *Manager) CreateDirectory(ctx context.Context, parent, name string) error {
	c, err := m.current()
	if err != nil {
		return err
	}
	_, err = c.CreateDirectory(ctx, protocol.ID(parent), name)
	return message(err)
}

// UploadFolder visits one directory at a time, preserving empty directories.
// Symlinks and special files are rejected rather than followed outside the tree.
func (m *Manager) UploadFolder(ctx context.Context, root, parent string) error {
	c, err := m.current()
	if err != nil {
		return err
	}
	var visit func(string, protocol.ID) error
	visit = func(path string, destination protocol.ID) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			j := job{client: c, parent: destination, path: path, item: Item{Name: filepath.Base(path), Direction: "upload"}}
			// Backpressure bounds pending files rather than truncating large trees.
			m.mu.Lock()
			j.item.ID, j.item.State = len(m.items)+1, "queued"
			m.items = append(m.items, j.item)
			m.mu.Unlock()
			m.emit(j.item)
			select {
			case m.queue <- j:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !info.IsDir() {
			return errors.New("folder upload does not follow symlinks or special files: " + path)
		}
		folder, err := c.CreateDirectory(ctx, destination, info.Name())
		if err != nil {
			return message(err)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				if err := visit(filepath.Join(path, entry.Name()), folder.ID); err != nil {
					return err
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					return nil
				}
				return readErr
			}
		}
	}
	return visit(filepath.Clean(root), protocol.ID(parent))
}

func (m *Manager) DeleteEntry(ctx context.Context, id string) error {
	c, err := m.current()
	if err != nil {
		return err
	}
	return message(c.DeleteEntry(ctx, protocol.ID(id)))
}

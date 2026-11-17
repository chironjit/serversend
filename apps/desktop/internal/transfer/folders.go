package transfer

import (
	"context"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
	"io"
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
// Links and unsupported special files are skipped visibly; genuine failures
// remain failures. Neither scanning nor the later worker follows source links.
func (m *Manager) UploadFolder(ctx context.Context, root, parent string) (err error) {
	c, err := m.current()
	if err != nil {
		return err
	}
	batchID := m.startBatch(filepath.Base(root))
	defer func() { m.endScan(batchID, err) }()
	var visit func(string, protocol.ID) error
	visit = func(path string, destination protocol.ID) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := source.Open(path)
		if skip, ok := source.Skipped(err); ok {
			m.skip(batchID, skip)
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			j := job{client: c, parent: destination, path: path, item: Item{BatchID: batchID, Name: filepath.Base(path), Direction: "upload"}}
			// Backpressure bounds pending files rather than truncating large trees.
			m.mu.Lock()
			j.item.ID, j.item.State = len(m.items)+1, "queued"
			m.items = append(m.items, j.item)
			m.queuedLocked(batchID)
			m.mu.Unlock()
			m.emit(j.item)
			select {
			case m.queue <- j:
				return nil
			case <-ctx.Done():
				m.update(j.item.ID, func(item *Item) { item.State = "failed"; item.Error = ctx.Err().Error() })
				m.finishFile(batchID, ctx.Err())
				return ctx.Err()
			}
		}
		folder, err := c.CreateDirectory(ctx, destination, info.Name())
		if err != nil {
			return message(err)
		}
		m.mu.Lock()
		m.batches[batchID].Directories++
		m.mu.Unlock()
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

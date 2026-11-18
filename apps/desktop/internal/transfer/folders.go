package transfer

import (
	"context"
	"github.com/chironjit/serversend/shared/client"
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
	m.mu.Lock()
	private := m.privateMode
	m.mu.Unlock()
	create := c.CreateDirectory
	if private {
		create = c.CreatePrivateDirectory
	}
	_, err = create(ctx, protocol.ID(parent), name)
	return message(err)
}

// UploadFolder visits one directory at a time, preserving empty directories.
// Links and unsupported special files are skipped visibly; genuine failures
// remain failures. Neither scanning nor the later worker follows source links.
func (m *Manager) UploadFolder(ctx context.Context, root, parent string) (err error) {
	if m.durableErr != nil {
		return m.durableErr
	}
	if m.durable != nil {
		return m.queueUploads([]string{root}, parent, true)
	}
	c, err := m.current()
	if err != nil {
		return err
	}
	batchID, ctx := m.startBatch(ctx, filepath.Base(root))
	defer func() { m.endScan(batchID, err) }()
	return m.scan(ctx, c, batchID, root, parent)
}

// StartFolderUpload returns once the selection is registered. Scanning waits
// on the bounded queue, so it continues in the background; its outcome is
// reported in Batches.
func (m *Manager) StartFolderUpload(root, parent string) error {
	if m.durableErr != nil {
		return m.durableErr
	}
	if m.durable != nil {
		return m.queueUploads([]string{root}, parent, true)
	}
	c, err := m.current()
	if err != nil {
		return err
	}
	batchID, ctx := m.startBatch(m.base(), filepath.Base(root))
	go func() { m.endScan(batchID, m.scan(ctx, c, batchID, root, parent)) }()
	return nil
}

func (m *Manager) scan(ctx context.Context, c *client.Client, batchID int, root, parent string) error {
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
			j := m.register(ctx, job{client: c, parent: destination, path: path, size: info.Size(), item: Item{BatchID: batchID, Name: filepath.Base(path), Direction: "upload", Total: info.Size()}})
			// Backpressure bounds pending files rather than truncating large trees.
			select {
			case m.queue <- j:
				return nil
			case <-ctx.Done():
				m.finish(j, ctx.Err())
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

package transfer

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (m *Manager) History(ctx context.Context, id, cursor string) (api.VersionPage, error) {
	c, e := m.current()
	if e != nil {
		return api.VersionPage{}, e
	}
	return c.History(ctx, protocol.ID(id), api.Cursor(cursor))
}
func (m *Manager) TrashPage(ctx context.Context, cursor string) (api.CatalogPage, error) {
	c, e := m.current()
	if e != nil {
		return api.CatalogPage{}, e
	}
	return c.TrashPage(ctx, api.Cursor(cursor))
}
func (m *Manager) RestoreTrash(ctx context.Context, id string) error {
	c, e := m.current()
	if e != nil {
		return e
	}
	return c.RestoreTrash(ctx, protocol.ID(id))
}
func (m *Manager) PurgeTrash(ctx context.Context, id string) error {
	c, e := m.current()
	if e != nil {
		return e
	}
	return c.PurgeTrash(ctx, protocol.ID(id))
}
func (m *Manager) SelectVersion(ctx context.Context, id, version string) error {
	m.mu.Lock()
	entry, ok := m.catalog[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("refresh the file list first")
	}
	c, e := m.current()
	if e != nil {
		return e
	}
	return c.SelectVersion(ctx, entry.ID, protocol.ID(version), entry.HeadRevision)
}
func (m *Manager) DownloadVersion(id, version, destination string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	m.mu.Lock()
	entry, ok := m.catalog[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("refresh the file list first")
	}
	if protocol.ID(version).Validate() != nil {
		return errors.New("invalid version")
	}
	entry.HeadVersionID = protocol.ID(version)
	if m.durableErr != nil {
		return m.durableErr
	}
	if m.durable != nil {
		return m.queueDownload(entry, destination, "replace")
	}
	c, e := m.current()
	if e != nil {
		return e
	}
	return m.enqueue(m.base(), job{client: c, path: destination, entry: entry, item: Item{Name: entry.Standard.Name, Direction: "download"}})
}

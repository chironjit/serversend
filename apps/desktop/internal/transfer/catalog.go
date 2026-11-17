package transfer

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

type FilePage struct {
	Files      []File `json:"files"`
	NextCursor string `json:"nextCursor"`
	Revision   string `json:"revision"`
	Snapshot   string `json:"snapshot"`
}

// FilesPage replaces the current page cache; selections and lists stay bounded.
func (m *Manager) FilesPage(ctx context.Context, parent, cursor string) (FilePage, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return FilePage{}, err
	}
	page, err := c.CatalogFolderPage(ctx, protocol.ID(parent), api.Cursor(cursor))
	if err != nil {
		return FilePage{}, err
	}
	files := make([]File, 0, len(page.Entries))
	catalog := make(map[string]api.CatalogEntry, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.Standard == nil || entry.Trashed {
			continue
		}
		catalog[string(entry.ID)] = entry
		files = append(files, File{ID: string(entry.ID), Name: entry.Standard.Name, Directory: entry.Kind == api.Directory})
	}
	m.mu.Lock()
	m.catalog = catalog
	m.mu.Unlock()
	sortFiles(files)
	return FilePage{Files: files, NextCursor: string(page.NextCursor), Revision: string(page.CatalogRevision), Snapshot: string(page.Snapshot)}, nil
}
func (m *Manager) CatalogChanged(ctx context.Context, revision string) (bool, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return false, err
	}
	_, err = c.CatalogChanges(ctx, protocol.Revision(revision))
	var typed api.Error
	if errors.As(err, &typed) && typed.Code == api.SnapshotExpired {
		return true, nil
	}
	return false, err
}

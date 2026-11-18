package transfer

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
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
		if entry.Mode == protocol.Private {
			name, e := c.PrivateName(ctx, entry)
			if errors.Is(e, vault.ErrLocked) {
				continue
			}
			if e != nil {
				return FilePage{}, e
			}
			entry.Standard = &protocol.StandardMetadata{Name: name}
		}
		if entry.Standard == nil || entry.Trashed {
			continue
		}
		catalog[string(entry.ID)] = entry
		files = append(files, File{ID: string(entry.ID), Name: entry.Standard.Name, Directory: entry.Kind == api.Directory})
	}
	m.mu.Lock()
	if _, err := c.VaultKey(); err != nil {
		filtered := files[:0]
		for _, f := range files {
			if catalog[f.ID].Mode != protocol.Private {
				filtered = append(filtered, f)
			} else {
				delete(catalog, f.ID)
			}
		}
		files = filtered
	}
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

// StorageStatistics is scoped by the live client session, never a frontend ID.
func (m *Manager) StorageStatistics(ctx context.Context) (api.StorageStatistics, error) {
	c, err := m.current()
	if err != nil {
		return api.StorageStatistics{}, err
	}
	return c.StorageStatistics(ctx)
}

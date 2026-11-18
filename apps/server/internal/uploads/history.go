package uploads

import (
	"context"
	"database/sql"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"strconv"
)

func (s *Service) History(ctx context.Context, account, file protocol.ID, request api.PageRequest) (api.VersionPage, error) {
	if file.Validate() != nil {
		return api.VersionPage{}, errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor, err := s.historyCursor(ctx, account, file, request)
	if err != nil {
		return api.VersionPage{}, err
	}
	versions, err := s.repo.History(ctx, account, file, cursor.After, request.Limit+1)
	if err != nil {
		return api.VersionPage{}, err
	}
	page := api.VersionPage{Page: api.Page{Snapshot: cursor.Snapshot}, FileID: file, Versions: versions}
	if len(versions) > request.Limit {
		page.Versions = versions[:request.Limit]
		cursor.After = page.Versions[len(page.Versions)-1].ID
		page.NextCursor = s.sealCatalogCursor(cursor)
	}
	return page, nil
}
func (s *Service) historyCursor(ctx context.Context, account, parent protocol.ID, request api.PageRequest) (catalogCursor, error) {
	if request.Validate() != nil {
		return catalogCursor{}, api.Error{Code: api.InvalidRequest, Message: "Invalid page"}
	}
	rev, err := s.repo.CatalogRevision(ctx, account)
	if err != nil {
		return catalogCursor{}, err
	}
	kind := "history:" + string(parent)
	if parent == "" {
		kind = "trash"
	}
	if request.Cursor != "" {
		cursor, err := s.openCatalogCursor(request.Cursor, account, parent, request.Limit)
		if err != nil {
			return cursor, err
		}
		if cursor.Kind != kind {
			return cursor, api.Error{Code: api.InvalidRequest, Message: "Wrong continuation type"}
		}
		if cursor.Revision != rev {
			return cursor, api.Error{Code: api.SnapshotExpired, Message: "Items changed; refresh this list"}
		}
		return cursor, nil
	}
	return catalogCursor{Kind: kind, Revision: rev, Instance: s.catalogInstance, Account: account, Parent: parent, Limit: request.Limit, Snapshot: newID(), Expires: s.now().Add(catalogSnapshotLifetime).Unix()}, nil
}
func (s *Service) TrashPage(ctx context.Context, account protocol.ID, request api.PageRequest) (api.CatalogPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor, err := s.historyCursor(ctx, account, "", request)
	if err != nil {
		return api.CatalogPage{}, err
	}
	entries, err := s.repo.TrashPage(ctx, account, cursor.After, request.Limit+1)
	if err != nil {
		return api.CatalogPage{}, err
	}
	for i := range entries {
		if err := s.repo.PrivateCatalog(ctx, account, &entries[i]); err != nil {
			return api.CatalogPage{}, err
		}
	}
	rev, err := s.repo.CatalogRevision(ctx, account)
	page := api.CatalogPage{Page: api.Page{Snapshot: cursor.Snapshot}, CatalogRevision: revision(rev), Entries: entries}
	if len(entries) > request.Limit {
		page.Entries = entries[:request.Limit]
		cursor.After = page.Entries[len(page.Entries)-1].ID
		page.NextCursor = s.sealCatalogCursor(cursor)
	}
	return page, err
}
func (s *Service) RestoreTrash(ctx context.Context, account, id protocol.ID) error {
	return s.lifecycle(ctx, func() error { return s.repo.RestoreTrash(ctx, account, id, s.now()) })
}
func (s *Service) PurgeTrash(ctx context.Context, account, id protocol.ID) error {
	return s.lifecycle(ctx, func() error {
		if s.readers > 0 {
			return api.Error{Code: api.Conflict, Message: "Downloads are active; retry purge when they finish"}
		}
		return s.repo.PurgeTrash(ctx, account, id, s.now())
	})
}
func (s *Service) SelectVersion(ctx context.Context, account, file, version protocol.ID, expected protocol.Revision) error {
	rev, err := strconv.ParseUint(string(expected), 10, 63)
	if err != nil || expected.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Expected revision is required"}
	}
	return s.lifecycle(ctx, func() error { return s.repo.SelectVersion(ctx, account, file, version, rev, s.now()) })
}
func (s *Service) lifecycle(ctx context.Context, op func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyJournal(ctx); err != nil {
		return err
	}
	if err := op(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	return s.applyJournal(ctx)
}
func (s *Service) Retention(ctx context.Context) (api.RetentionPolicy, error) {
	return s.repo.Retention(ctx)
}
func (s *Service) SetRetention(ctx context.Context, p api.RetentionPolicy) error {
	return s.lifecycle(ctx, func() error { return s.repo.SetRetention(ctx, p) })
}

func (s *Service) UploadTarget(ctx context.Context, account, parent protocol.ID, name string) (*api.CatalogEntry, error) {
	if protocol.ValidateName(name) != nil || parent != "" && parent.Validate() != nil {
		return nil, api.Error{Code: api.InvalidRequest, Message: "Invalid upload destination"}
	}
	return s.repo.UploadTarget(ctx, account, parent, name)
}

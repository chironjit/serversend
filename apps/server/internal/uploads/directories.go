package uploads

import (
	"context"
	"fmt"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// CreateDirectory creates a folder, or returns the existing folder of that
// name, and creates it in the data directory.
func (s *Service) CreateDirectory(ctx context.Context, account protocol.ID, request api.CreateDirectoryRequest) (api.CatalogEntry, error) {
	if request.Validate(nil) != nil || request.Mode != protocol.Standard {
		return api.CatalogEntry{}, api.Error{Code: api.InvalidRequest, Message: "Only valid standard folders are supported"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyJournal(ctx); err != nil {
		return api.CatalogEntry{}, err
	}
	entry, err := s.repo.CreateDirectory(ctx, account, request)
	if err != nil {
		return api.CatalogEntry{}, err
	}
	return entry, s.applyJournal(ctx)
}

// DeleteEntry permanently removes a file or folder tree, its retained
// versions and unfinished uploads into it.
func (s *Service) DeleteEntry(ctx context.Context, account, id protocol.ID) error {
	if id.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid file or folder ID"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyJournal(ctx); err != nil {
		return err
	}
	if err := s.repo.DeleteEntry(ctx, account, id); err != nil {
		return err
	}
	if err := s.applyJournal(ctx); err != nil {
		return fmt.Errorf("remove deleted files: %w", err)
	}
	return nil
}

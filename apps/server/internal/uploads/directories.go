package uploads

import (
	"context"
	"fmt"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (s *Service) CreateDirectory(ctx context.Context, account protocol.ID, request api.CreateDirectoryRequest) (api.CatalogEntry, error) {
	if request.Validate(nil) != nil || request.Mode != protocol.Standard {
		return api.CatalogEntry{}, api.Error{Code: api.InvalidRequest, Message: "Only valid standard folders are supported"}
	}
	return s.repo.CreateDirectory(ctx, account, request)
}

func (s *Service) DeleteEntry(ctx context.Context, account, id protocol.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid file or folder ID"}
	}
	if _, err := s.repo.DeleteEntry(ctx, account, id); err != nil {
		return err
	}
	if err := s.cleanupDeleted(ctx); err != nil {
		return fmt.Errorf("clean up deleted file objects: %w", err)
	}
	return nil
}
func (s *Service) cleanupDeleted(ctx context.Context) error {
	for {
		ids, err := s.repo.DeletedObjects(ctx)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			referenced, err := s.repo.ObjectReferenced(ctx, id)
			if err != nil {
				return err
			}
			if referenced {
				return fmt.Errorf("refuse to delete referenced object %s", id)
			}
			if err = s.store.Delete(id); err != nil {
				return err
			}
			if err = s.repo.ForgetDeletedObject(ctx, id); err != nil {
				return err
			}
		}
	}
}

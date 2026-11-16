package uploads

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (s *Service) CreateDirectory(ctx context.Context, account protocol.ID, request api.CreateDirectoryRequest) (api.CatalogEntry, error) {
	if request.Validate(nil) != nil || request.Mode != protocol.Standard {
		return api.CatalogEntry{}, api.Error{Code: api.InvalidRequest, Message: "Only valid standard folders are supported"}
	}
	return s.repo.CreateDirectory(ctx, account, request)
}

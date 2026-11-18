package auth

import (
	"context"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

func (s *Service) Users(ctx context.Context, token string) ([]database.UserSummary, error) {
	session, err := s.Authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	if session.Role != "admin" {
		return nil, ErrForbidden
	}
	return s.repo.Users(ctx)
}

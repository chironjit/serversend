package auth

import (
	"context"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/protocol"
)

func (s *Service) Users(ctx context.Context, token string) ([]database.UserSummary, error) {
	session, err := s.Authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	if session.Role != "admin" {
		return nil, ErrForbidden
	}
	users, err := s.repo.Users(ctx, s.now())
	if err != nil {
		return nil, err
	}
	for i := range users {
		u := &users[i]
		if u.State == "password_required" && !u.Disabled && u.ExpiresAt != nil && u.ExpiresAt.After(s.now()) && len(u.Protected) > 0 {
			u.TemporaryPassword, err = s.protector.OpenPassword(protocol.Scope{ServerID: s.serverID, AccountID: u.ID}, u.Protected)
			if err != nil {
				return nil, err
			}
		}
		u.Protected = nil
	}
	return users, nil
}

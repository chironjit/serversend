package auth

import (
	"context"
	"database/sql"
	"errors"
)

// CreateUserFromSession requires a full sign-in within the last five minutes.
func (s *Service) CreateUserFromSession(ctx context.Context, token, username string) (IssuedPassword, error) {
	if !validToken(token) {
		return IssuedPassword{}, ErrCredentials
	}
	session, err := s.repo.SessionByToken(ctx, accessHash(token), s.now())
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedPassword{}, ErrCredentials
	}
	if err != nil {
		return IssuedPassword{}, err
	}
	if session.Role != "admin" {
		return IssuedPassword{}, ErrForbidden
	}
	return s.CreateUser(ctx, Principal{accountID: session.AccountID, epoch: session.Epoch, reauthenticatedAt: session.ExpiresAt.Add(-SessionLifetime)}, username)
}

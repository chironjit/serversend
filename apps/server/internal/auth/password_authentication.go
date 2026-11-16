package auth

import (
	"context"
	"database/sql"
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"strings"
	"time"
)

type Attempt = api.PasswordAttempt

func (s *Service) AuthenticatePassword(ctx context.Context, username, password string) (Attempt, error) {
	if len(username) > 128 || len(password) > 256 {
		return Attempt{}, ErrCredentials
	}
	release, err := s.acquire()
	if err != nil {
		return Attempt{}, err
	}
	defer release()
	for _, c := range username {
		if c > 127 {
			username = ""
			break
		}
	}
	c, err := s.repo.CredentialsByUsername(ctx, strings.ToLower(username))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, err
	}
	now := s.now().UTC()
	if err != nil {
		s.passwords.Verify(s.dummyHash, password)
		return Attempt{}, ErrCredentials
	}
	if c.BlockedUntil > now.UnixMilli() {
		s.passwords.Verify(s.dummyHash, password)
		return Attempt{}, ErrCredentials
	}
	valid := s.passwords.Verify(c.PasswordHash, password)
	if !valid || c.Status.Disabled || c.Status.State == protocol.PasswordRequired && (!c.TemporaryExpiresAt.Valid || c.TemporaryExpiresAt.Int64 <= now.UnixMilli()) {
		if err := s.repo.RecordPasswordFailure(ctx, c, now); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Attempt{}, err
		}
		return Attempt{}, ErrCredentials
	}
	scope := "second_factor"
	if c.Status.State == protocol.PasswordRequired {
		scope = "replace_password"
	} else if c.Status.State == protocol.TOTPRequired {
		scope = "enroll_totp"
	}
	token := newSecret()
	expires := now.Add(ChallengeLifetime)
	if scope == "replace_password" {
		expires = minTime(expires, time.UnixMilli(c.TemporaryExpiresAt.Int64))
	}
	challenge := database.AuthChallenge{TokenHash: tokenHash(token), AccountID: c.ID, Scope: scope, Revision: c.Revision, Epoch: c.Epoch, ExpiresAt: expires}
	if err := s.repo.BeginChallenge(ctx, c, challenge, now); err != nil {
		if errors.Is(err, database.ErrAuthConflict) {
			return Attempt{}, ErrCredentials
		}
		return Attempt{}, err
	}
	return Attempt{Challenge: token, NextStep: scope, ExpiresAt: expires}, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Service) ReplacePassword(ctx context.Context, token, password string) error {
	if !validToken(token) {
		return ErrCredentials
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()
	c, ch, err := s.repo.Challenge(ctx, tokenHash(token), s.now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, database.ErrAuthConflict) {
			return ErrCredentials
		}
		return err
	}
	if ch.Scope != "replace_password" || c.Status.State != protocol.PasswordRequired {
		return ErrCredentials
	}
	if s.passwords.Verify(c.PasswordHash, password) {
		return ErrPasswordPolicy
	}
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return err
	}
	if err := s.repo.ReplacePassword(ctx, ch, hash, s.now().UTC()); err != nil {
		if errors.Is(err, database.ErrAuthConflict) {
			return ErrCredentials
		}
		return err
	}
	return nil
}

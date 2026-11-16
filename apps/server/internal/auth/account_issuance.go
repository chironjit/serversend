package auth

import (
	"context"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"strings"
)

// IssuedPassword must never be logged or persisted in plaintext.
type IssuedPassword = api.TemporaryPassword

func (s *Service) BootstrapAdmin(ctx context.Context, username string) (IssuedPassword, error) {
	return s.issue(ctx, Principal{}, username, "admin")
}

// RecoverBootstrapAdmin must only be called through an explicit local operator
// command. It never recovers an active account or opens network setup access.
func (s *Service) RecoverBootstrapAdmin(ctx context.Context, username string) (IssuedPassword, error) {
	for _, c := range username {
		if c > 127 {
			return IssuedPassword{}, ErrInput
		}
	}
	username = strings.ToLower(username)
	if !validUsername(username) {
		return IssuedPassword{}, ErrInput
	}
	release, err := s.acquire()
	if err != nil {
		return IssuedPassword{}, err
	}
	defer release()
	password := newSecret()
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return IssuedPassword{}, err
	}
	now := s.now().UTC()
	expires := now.Add(TemporaryPasswordLifetime)
	id, err := s.repo.RecoverBootstrap(ctx, username, hash, expires, now)
	if err != nil {
		return IssuedPassword{}, err
	}
	return IssuedPassword{AccountID: id, Username: username, TemporaryPassword: password, ExpiresAt: expires}, nil
}

func (s *Service) CreateUser(ctx context.Context, actor Principal, username string) (IssuedPassword, error) {
	if !s.reauthenticated(actor) {
		return IssuedPassword{}, ErrForbidden
	}
	return s.issue(ctx, actor, username, "user")
}

func (s *Service) issue(ctx context.Context, actor Principal, username, role string) (IssuedPassword, error) {
	for _, c := range username {
		if c > 127 {
			return IssuedPassword{}, ErrInput
		}
	}
	username = strings.ToLower(username)
	if !validUsername(username) {
		return IssuedPassword{}, ErrInput
	}
	release, err := s.acquire()
	if err != nil {
		return IssuedPassword{}, err
	}
	defer release()
	password := newSecret()
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return IssuedPassword{}, err
	}
	now := s.now().UTC()
	issued := IssuedPassword{AccountID: newID(), Username: username, TemporaryPassword: password, ExpiresAt: now.Add(TemporaryPasswordLifetime)}
	account := database.Account{ID: issued.AccountID, Username: username, Role: role, Status: protocol.AccountStatus{State: protocol.PasswordRequired}, CreatedAt: now}
	var protected []byte
	if role == "user" && s.protector != nil {
		protected, err = s.protector.SealPassword(protocol.Scope{ServerID: s.serverID, AccountID: account.ID}, password)
		if err != nil {
			return IssuedPassword{}, err
		}
	}
	err = s.repo.IssueAccount(ctx, account, hash, issued.ExpiresAt, actor.accountID, actor.epoch, protected)
	if err != nil {
		return IssuedPassword{}, err
	}
	return issued, nil
}

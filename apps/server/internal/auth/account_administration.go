package auth

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"time"
)

func (s *Service) reauthenticated(actor Principal) bool {
	now := s.now()
	return actor.accountID.Validate() == nil && !actor.reauthenticatedAt.IsZero() && !actor.reauthenticatedAt.After(now) && now.Sub(actor.reauthenticatedAt) <= 5*time.Minute
}

func (s *Service) ResetPassword(ctx context.Context, actor Principal, target protocol.ID) (IssuedPassword, error) {
	if !s.reauthenticated(actor) {
		return IssuedPassword{}, ErrForbidden
	}
	if target.Validate() != nil {
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
	if err := s.repo.ResetPassword(ctx, actor.accountID, actor.epoch, target, hash, expires, now); err != nil {
		return IssuedPassword{}, err
	}
	return IssuedPassword{AccountID: target, TemporaryPassword: password, ExpiresAt: expires}, nil
}

func (s *Service) SetDisabled(ctx context.Context, actor Principal, target protocol.ID, disabled bool) error {
	if !s.reauthenticated(actor) {
		return ErrForbidden
	}
	if target.Validate() != nil {
		return ErrInput
	}
	return s.repo.SetDisabled(ctx, actor.accountID, actor.epoch, target, disabled, s.now().UTC())
}

func (s *Service) SetUserDisabled(ctx context.Context, token string, target protocol.ID, disabled bool) error {
	actor, err := s.adminPrincipal(ctx, token)
	if err != nil {
		return err
	}
	if err = s.userTarget(ctx, target); err != nil {
		return err
	}
	return s.SetDisabled(ctx, actor, target, disabled)
}

func (s *Service) AccountAudit(ctx context.Context, token string, before int64) (api.AccountAuditPage, error) {
	session, err := s.Authorize(ctx, token)
	if err != nil {
		return api.AccountAuditPage{}, err
	}
	if session.Role != "admin" {
		return api.AccountAuditPage{}, ErrForbidden
	}
	return s.repo.AccountAudit(ctx, before)
}

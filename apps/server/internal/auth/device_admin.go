package auth

import (
	"context"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/protocol"
)

func (s *Service) Devices(ctx context.Context, token string, target protocol.ID) ([]database.DeviceInfo, error) {
	if target.Validate() != nil {
		return nil, ErrInput
	}
	session, err := s.Authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	if session.Role != "admin" {
		return nil, ErrForbidden
	}
	return s.repo.Devices(ctx, target, s.now())
}
func (s *Service) RevokeDevice(ctx context.Context, token string, target, device protocol.ID) error {
	if target.Validate() != nil || device.Validate() != nil {
		return ErrInput
	}
	actor, err := s.adminPrincipal(ctx, token)
	if err != nil {
		return err
	}
	return s.repo.RevokeDevice(ctx, actor.accountID, actor.epoch, target, device, s.now().UTC())
}
func (s *Service) ResetUserPassword(ctx context.Context, token string, target protocol.ID) (IssuedPassword, error) {
	actor, err := s.adminPrincipal(ctx, token)
	if err != nil {
		return IssuedPassword{}, err
	}
	if err = s.userTarget(ctx, target); err != nil {
		return IssuedPassword{}, err
	}
	return s.ResetPassword(ctx, actor, target)
}
func (s *Service) ResetUserTOTP(ctx context.Context, token string, target protocol.ID) error {
	actor, err := s.adminPrincipal(ctx, token)
	if err != nil {
		return err
	}
	if err = s.userTarget(ctx, target); err != nil {
		return err
	}
	return s.ResetTOTP(ctx, actor, target)
}
func (s *Service) userTarget(ctx context.Context, target protocol.ID) error {
	if target.Validate() != nil {
		return ErrInput
	}
	account, err := s.repo.GetAccount(ctx, target)
	if err != nil {
		return err
	}
	if account.Role != "user" {
		return ErrForbidden
	}
	return nil
}

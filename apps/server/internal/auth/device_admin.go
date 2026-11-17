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

func (s *Service) OwnDevices(ctx context.Context, token string, before ...protocol.ID) ([]database.DeviceInfo, error) {
	if len(before) > 0 && before[0] != "" && before[0].Validate() != nil {
		return nil, ErrInput
	}
	caller, err := s.Authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	devices, err := s.repo.Devices(ctx, caller.AccountID, s.now(), before...)
	for i := range devices {
		devices[i].Current = devices[i].ID == caller.DeviceID
	}
	return devices, err
}
func (s *Service) RevokeOwnDevice(ctx context.Context, token string, device protocol.ID, others bool) error {
	if !others && device.Validate() != nil {
		return ErrInput
	}
	if !validToken(token) {
		return ErrCredentials
	}
	source, err := s.repo.SessionByToken(ctx, accessHash(token), s.now())
	if err != nil {
		return refreshError(err)
	}
	return refreshError(s.repo.RevokeOwnDevices(ctx, source, accessHash(token), device, others, s.now().UTC()))
}

package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
)

func refreshHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("ServerSend refresh v1\x00" + token))
	return hex.EncodeToString(sum[:])
}
func refreshError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, database.ErrAuthConflict) {
		return ErrCredentials
	}
	return err
}
func (s *Service) Refresh(ctx context.Context, q api.RefreshRequest) (api.RefreshResult, error) {
	if q.DeviceID.Validate() != nil || q.RotationID.Validate() != nil || !validToken(q.RefreshCredential) || !validToken(q.NextRefreshCredential) || q.RefreshCredential == q.NextRefreshCredential || len(q.Code) > 64 {
		return api.RefreshResult{}, ErrCredentials
	}
	release, err := s.acquire()
	if err != nil {
		return api.RefreshResult{}, err
	}
	defer func() { release() }()
	now := s.now().UTC()
	old, next := refreshHash(q.RefreshCredential), refreshHash(q.NextRefreshCredential)
	p, err := s.repo.CheckRefresh(ctx, q.DeviceID, old, next, string(q.RotationID), now)
	if err != nil {
		return api.RefreshResult{}, refreshError(err)
	}
	reauth, recovery := false, false
	if !p.LastFactor.Add(database.FactorLifetime).After(now) {
		if q.Challenge == "" || q.Code == "" {
			challenge := newSecret()
			if err = s.repo.BeginDeviceChallenge(ctx, p, tokenHash(challenge), now); err != nil {
				return api.RefreshResult{}, refreshError(err)
			}
			return api.RefreshResult{NextStep: "second_factor", Challenge: challenge}, nil
		}
		_, ch, err := s.repo.Challenge(ctx, tokenHash(q.Challenge), now)
		if err != nil || ch.DeviceID != p.ID || ch.AccountID != p.AccountID || ch.Epoch != p.Epoch {
			return api.RefreshResult{}, ErrCredentials
		}
		// VerifySecondFactor owns its own bounded worker permit.
		release()
		release = func() {} // release before nested verification
		verified, err := s.VerifySecondFactor(ctx, q.Challenge, q.Code)
		if err != nil {
			return api.RefreshResult{}, err
		}
		if verified.AccountID != p.AccountID || verified.Epoch != p.Epoch {
			return api.RefreshResult{}, ErrCredentials
		}
		reauth = true
		recovery = verified.UsedRecoveryCode
	}
	token := newSecret()
	out, expiry, err := s.repo.RotateRefresh(ctx, p, old, next, string(q.RotationID), accessHash(token), reauth, recovery, s.now().UTC())
	if errors.Is(err, database.ErrReauthentication) {
		challenge := newSecret()
		if err = s.repo.BeginDeviceChallenge(ctx, p, tokenHash(challenge), s.now().UTC()); err != nil {
			return api.RefreshResult{}, refreshError(err)
		}
		return api.RefreshResult{NextStep: "second_factor", Challenge: challenge}, nil
	}
	if err != nil {
		return api.RefreshResult{}, refreshError(err)
	}
	session := api.Session{Role: out.Role, ServerID: s.serverID, AccountID: out.AccountID, DeviceID: out.ID, Username: out.Username, AccessToken: token, ExpiresAt: expiry, IdleExpiresAt: out.LastRefresh.Add(database.RefreshIdleLifetime), ReauthenticateAt: out.LastFactor.Add(database.FactorLifetime)}
	return api.RefreshResult{Session: &session}, nil
}
func (s *Service) RevokeRefresh(ctx context.Context, q api.RefreshRequest) error {
	if q.DeviceID.Validate() != nil || !validToken(q.RefreshCredential) {
		return ErrCredentials
	}
	return s.repo.RevokeByRefresh(ctx, q.DeviceID, refreshHash(q.RefreshCredential), s.now().UTC())
}

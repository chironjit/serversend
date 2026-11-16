package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// SessionLifetime is the vertical-slice device session. There is no refresh
// yet: Chunk 07 replaces this with short-lived access tokens and rotating
// refresh credentials, after which a device stays signed in.
const SessionLifetime = 12 * time.Hour

// Session is the authenticated caller of one request. It is derived from live
// account state each time and must not be cached across requests.
type Session struct {
	DeviceID  protocol.ID
	AccountID protocol.ID
	Username  string
	Role      string
}

func accessHash(token string) string {
	sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) ServerID() protocol.ID { return s.serverID }

// CreateSession is the only way to obtain an access token: it consumes a
// second-factor challenge with a valid TOTP or recovery code first.
func (s *Service) CreateSession(ctx context.Context, challenge, code, deviceName string) (api.Session, error) {
	if api.ValidateDeviceName(deviceName) != nil {
		return api.Session{}, ErrInput
	}
	verified, err := s.VerifySecondFactor(ctx, challenge, code)
	if err != nil {
		return api.Session{}, err
	}
	account, err := s.repo.GetAccount(ctx, verified.AccountID)
	if err != nil {
		return api.Session{}, err
	}
	token, now := newSecret(), s.now().UTC()
	session := database.DeviceSession{ID: newID(), AccountID: verified.AccountID, Name: deviceName, Epoch: verified.Epoch, ExpiresAt: now.Add(SessionLifetime)}
	if err := s.repo.CreateSession(ctx, session, accessHash(token), now); err != nil {
		return api.Session{}, conflictAsCredentials(err)
	}
	return api.Session{Role: account.Role, DeviceID: session.ID, ServerID: s.serverID, AccountID: account.ID, Username: account.Username, AccessToken: token, ExpiresAt: session.ExpiresAt}, nil
}

// Authorize resolves an access token against live state. Expired sessions and
// accounts that were disabled or reset since sign-in fail identically.
func (s *Service) Authorize(ctx context.Context, token string) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrCredentials
	}
	session, err := s.repo.SessionByToken(ctx, accessHash(token), s.now())
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrCredentials
	}
	if err != nil {
		return Session{}, err
	}
	return Session{DeviceID: session.ID, AccountID: session.AccountID, Username: session.Username, Role: session.Role}, nil
}

// EndSession signs this device out. Unknown tokens are not an error.
func (s *Service) EndSession(ctx context.Context, token string) error {
	if !validToken(token) {
		return nil
	}
	return s.repo.DeleteSession(ctx, accessHash(token))
}

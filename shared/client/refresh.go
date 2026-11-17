package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

var ErrTOTPRequired = errors.New("verify your authenticator to continue using this device")

// CredentialState is native-only keychain material. Access tokens are omitted
// from persisted state. Pending tuples make lost refresh responses recoverable.
type CredentialState struct {
	Session api.Session         `json:"session"`
	Pending *api.RefreshRequest `json:"pending,omitempty"`
}

func (c *Client) SetCredentialStore(store func(CredentialState) error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.store = store
}
func (c *Client) credentialStateLocked() CredentialState {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	session.AccessToken = ""
	var pending *api.RefreshRequest
	if c.pending != nil {
		copy := *c.pending
		copy.Code = ""
		copy.Challenge = ""
		pending = &copy
	}
	return CredentialState{Session: session, Pending: pending}
}
func (c *Client) Credentials() CredentialState {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.credentialStateLocked()
}
func (c *Client) persistLocked() error {
	if c.store == nil {
		return nil
	}
	return c.store(c.credentialStateLocked())
}
func (c *Client) EnsureSession(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session.RefreshCredential == "" {
		return nil
	}
	if c.refreshChallenge != "" && c.refreshChallengeExpires.After(time.Now()) {
		return ErrTOTPRequired
	}
	if c.pending == nil && session.AccessToken != "" && session.ExpiresAt.After(time.Now().Add(time.Minute)) && session.ReauthenticateAt.After(time.Now()) {
		return nil
	}
	return c.rotateLocked(ctx, "")
}
func (c *Client) Reauthenticate(ctx context.Context, code string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.rotateLocked(ctx, code)
}
func (c *Client) rotateLocked(ctx context.Context, code string) error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session.RefreshCredential == "" {
		return errors.New("sign in again")
	}
	if c.pending == nil {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		c.pending = &api.RefreshRequest{DeviceID: session.DeviceID, RefreshCredential: session.RefreshCredential, NextRefreshCredential: base64.RawURLEncoding.EncodeToString(secret), RotationID: protocol.ID(hex.EncodeToString(id))}
	}
	// This must succeed before sending the successor to the server, including retries.
	if err := c.persistLocked(); err != nil {
		return err
	}
	if !c.refreshChallengeExpires.After(time.Now()) {
		c.refreshChallenge = ""
	}
	request := *c.pending
	request.Challenge = c.refreshChallenge
	request.Code = code
	var result api.RefreshResult
	for attempt := 0; ; attempt++ {
		err := c.exchange(ctx, call{method: "POST", path: "/sessions/refresh"}, request, &result)
		if err == nil {
			break
		}
		var typed api.Error
		if attempt >= 2 || !errors.As(err, &typed) || typed.Code != api.Overloaded {
			return err
		}
		delay := time.Duration(min(5, max(1, typed.RetryAfterSeconds))) * time.Second
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if result.NextStep == "second_factor" && result.Challenge != "" {
		c.refreshChallenge = result.Challenge
		c.refreshChallengeExpires = time.Now().Add(9 * time.Minute)
		return ErrTOTPRequired
	}
	if result.Session == nil || result.Session.AccessToken == "" || result.Session.ServerID != session.ServerID || result.Session.AccountID != session.AccountID || result.Session.DeviceID != session.DeviceID {
		return errors.New("server sent an invalid refreshed session")
	}
	refreshed := *result.Session
	refreshed.RefreshCredential = c.pending.NextRefreshCredential
	// Keep the pending tuple until the committed successor is safely persisted.
	oldPending := c.pending
	c.mu.Lock()
	c.session = refreshed
	c.mu.Unlock()
	c.pending = nil
	if err := c.persistLocked(); err != nil {
		c.pending = oldPending
		return err
	}
	c.refreshChallenge = ""
	c.refreshChallengeExpires = time.Time{}
	return nil
}
func (c *Client) RestoreCredentials(ctx context.Context, state CredentialState) error {
	if state.Session.RefreshCredential == "" {
		return errors.New("sign in again to enable device refresh")
	}
	if (protocol.Scope{ServerID: state.Session.ServerID, AccountID: state.Session.AccountID}).Validate() != nil || state.Session.DeviceID.Validate() != nil {
		return errors.New("invalid saved device")
	}
	c.refreshMu.Lock()
	c.mu.Lock()
	c.session = state.Session
	c.session.AccessToken = ""
	c.mu.Unlock()
	if state.Pending != nil {
		pending := *state.Pending
		c.pending = &pending
	} else {
		c.pending = nil
	}
	c.refreshMu.Unlock()
	return c.EnsureSession(ctx)
}

// Heartbeat records recent connectivity without independently extending any deadline.
func (c *Client) Heartbeat(ctx context.Context) error {
	if err := c.EnsureSession(ctx); err != nil {
		return err
	}
	return c.exchange(ctx, call{method: "GET", path: "/sessions/current", token: c.token()}, nil, nil)
}

func (c *Client) SetAppVersion(version string) { c.appVersion = version }

package api

import (
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chironjit/serversend/shared/protocol"
)

// SessionRequest completes sign-in: six TOTP digits or one recovery code,
// plus a label the user will recognize in a device list.
type SessionRequest struct {
	InstallationID protocol.ID `json:"installation_id,omitempty"`
	Platform       string      `json:"platform,omitempty"`
	AppVersion     string      `json:"app_version,omitempty"`
	Code           string      `json:"code"`
	DeviceName     string      `json:"device_name"`
}

// Session carries device authorization. Both AccessToken and RefreshCredential are credentials:
// keep it out of logs and frontend state. The scope identifies which
// server/account every later identifier belongs to.
type Session struct {
	CSRFToken         string      `json:"csrf_token,omitempty"`
	RefreshCredential string      `json:"refresh_credential,omitempty"`
	IdleExpiresAt     time.Time   `json:"idle_expires_at,omitempty"`
	ReauthenticateAt  time.Time   `json:"reauthenticate_at,omitempty"`
	Role              string      `json:"role"`
	DeviceID          protocol.ID `json:"device_id"`
	ServerID          protocol.ID `json:"server_id"`
	AccountID         protocol.ID `json:"account_id"`
	Username          string      `json:"username"`
	AccessToken       string      `json:"access_token"`
	ExpiresAt         time.Time   `json:"expires_at"`
}

func ValidateDeviceName(name string) error {
	if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("device name must be 1 to 128 bytes without control characters or surrounding spaces")
	}
	return nil
}

func DecodeSessionRequest(r io.Reader) (SessionRequest, error) {
	var request SessionRequest
	if err := decode(r, MaxAuthenticationBytes, &request); err != nil {
		return request, err
	}
	if request.InstallationID != "" && request.InstallationID.Validate() != nil {
		return request, errors.New("invalid installation identifier")
	}
	if len(request.Platform) > 128 || len(request.AppVersion) > 128 {
		return request, errors.New("device metadata exceeds limit")
	}
	if request.Code == "" || len(request.Code) > 64 {
		return request, errors.New("code is required within its length limit")
	}
	return request, ValidateDeviceName(request.DeviceName)
}

// RefreshRequest is a durable, device-bound rotation tuple. Never log it.
type RefreshRequest struct {
	DeviceID              protocol.ID `json:"device_id"`
	RefreshCredential     string      `json:"refresh_credential"`
	NextRefreshCredential string      `json:"next_refresh_credential"`
	RotationID            protocol.ID `json:"rotation_id"`
	Challenge             string      `json:"challenge,omitempty"`
	Code                  string      `json:"code,omitempty"`
}
type RefreshResult struct {
	Session   *Session `json:"session,omitempty"`
	Challenge string   `json:"challenge,omitempty"`
	NextStep  string   `json:"next_step,omitempty"`
}

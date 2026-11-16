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
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}

// Session carries a device access token. Treat AccessToken as a credential:
// keep it out of logs and frontend state. The scope identifies which
// server/account every later identifier belongs to.
type Session struct {
	Role        string      `json:"role"`
	DeviceID    protocol.ID `json:"device_id"`
	ServerID    protocol.ID `json:"server_id"`
	AccountID   protocol.ID `json:"account_id"`
	Username    string      `json:"username"`
	AccessToken string      `json:"access_token"`
	ExpiresAt   time.Time   `json:"expires_at"`
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
	if request.Code == "" || len(request.Code) > 64 {
		return request, errors.New("code is required within its length limit")
	}
	return request, ValidateDeviceName(request.DeviceName)
}

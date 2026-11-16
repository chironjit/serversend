package api

import (
	"errors"
	"io"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

const MaxAuthenticationBytes int64 = 4096

type PasswordAuthentication struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// PasswordAttempt is restricted to NextStep; it is never an access token.
type PasswordAttempt struct {
	Challenge string    `json:"challenge"`
	NextStep  string    `json:"next_step"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PasswordReplacement struct {
	Password string `json:"password"`
}

// TemporaryPassword is shown once to the operator, never logged. Bootstrap
// emits it through a local command; it is not a public setup response.
type TemporaryPassword struct {
	AccountID         protocol.ID `json:"account_id"`
	Username          string      `json:"username,omitempty"`
	TemporaryPassword string      `json:"temporary_password"`
	ExpiresAt         time.Time   `json:"expires_at"`
}

func DecodePasswordAuthentication(r io.Reader) (PasswordAuthentication, error) {
	var request PasswordAuthentication
	if err := decode(r, MaxAuthenticationBytes, &request); err != nil {
		return request, err
	}
	if request.Username == "" || len(request.Username) > 128 || request.Password == "" || len(request.Password) > 256 {
		return request, errors.New("username and password are required within their length limits")
	}
	return request, nil
}

func DecodePasswordReplacement(r io.Reader) (PasswordReplacement, error) {
	var request PasswordReplacement
	if err := decode(r, MaxAuthenticationBytes, &request); err != nil {
		return request, err
	}
	if request.Password == "" || len(request.Password) > 256 {
		return request, errors.New("password is required within its length limit")
	}
	return request, nil
}

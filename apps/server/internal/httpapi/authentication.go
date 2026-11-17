package httpapi

import (
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/applog"
	"mime"
	"net/http"
	"strings"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/uploads"
	"github.com/chironjit/serversend/shared/api"
)

type options struct {
	authentication *auth.Service
	transfers      *uploads.Service
	http3          bool
	logs           *applog.Store
}
type Option func(*options)

// WithHTTP3 advertises QUIC only for a process with an active UDP listener.
func WithHTTP3() Option { return func(o *options) { o.http3 = true } }

func WithAuthentication(service *auth.Service) Option {
	return func(o *options) { o.authentication = service }
}

const (
	passwordPath        = api.BasePath + "/auth/password"
	passwordReplacePath = api.BasePath + "/auth/password/replace"
	totpEnrollPath      = api.BasePath + "/auth/totp/enroll"
	totpConfirmPath     = api.BasePath + "/auth/totp/confirm"
)

// serveAuthentication recognizes only restricted onboarding attempts. No
// access token, cookie, device registration or admin permission is issued
// here; second-factor login belongs to the session endpoints.
func serveAuthentication(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	switch r.URL.Path {
	case passwordPath, passwordReplacePath, totpEnrollPath, totpConfirmPath:
	default:
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, r, 405, api.InvalidRequest, "Use POST for this endpoint")
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, r, 403, api.PermissionDenied, "Use the server's own secure origin for this request")
		return true
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || r.Header.Get("Content-Encoding") != "" {
		writeError(w, r, 415, api.InvalidRequest, "Send an uncompressed application/json request")
		return true
	}
	if service == nil {
		writeError(w, r, 503, api.Unavailable, "Authentication is not configured; contact the server operator")
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxAuthenticationBytes)
	defer r.Body.Close()
	if r.URL.Path == passwordPath {
		request, err := api.DecodePasswordAuthentication(r.Body)
		if err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		attempt, err := service.AuthenticatePassword(r.Context(), request.Username, request.Password)
		if err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, attempt)
		return true
	}
	parts := strings.Split(r.Header.Get("Authorization"), " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		writeAuthenticationError(w, r, auth.ErrCredentials)
		return true
	}
	token := parts[1]
	switch r.URL.Path {
	case passwordReplacePath:
		request, err := api.DecodePasswordReplacement(r.Body)
		if err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		if err := service.ReplacePassword(r.Context(), token, request.Password); err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	case totpEnrollPath:
		if err := api.DecodeEmptyRequest(r.Body); err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		enrollment, err := service.BeginTOTPEnrollment(r.Context(), token)
		if err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, enrollment)
	case totpConfirmPath:
		request, err := api.DecodeSecondFactorCode(r.Body)
		if err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		codes, err := service.ConfirmTOTPEnrollment(r.Context(), token, request.Code)
		if err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, api.RecoveryCodes{Codes: codes})
	}
	return true
}

func writeAuthenticationDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.Is(err, api.ErrResponseTooLarge) || errors.As(err, &tooLarge) {
		writeError(w, r, 413, api.RequestTooLarge, "Authentication request is too large")
		return
	}
	writeError(w, r, 400, api.InvalidRequest, "Send a valid authentication request with the required fields")
}

func writeAuthenticationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, database.ErrRecoveryPending):
		writeError(w, r, 409, api.Conflict, "Complete the pending recovery before resetting the other factor")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, database.ErrAdminRequired):
		writeError(w, r, 403, api.PermissionDenied, "Sign in as an administrator again; this action requires authentication within five minutes")
	case errors.Is(err, auth.ErrInput):
		writeError(w, r, 400, api.InvalidRequest, "Use a valid username")
	case errors.Is(err, database.ErrUsernameExists):
		writeError(w, r, 409, api.Conflict, "This username already exists")
	case errors.Is(err, auth.ErrCredentials):
		writeError(w, r, 401, api.AuthenticationRequired, "Credentials are invalid or expired; sign in again")
	case errors.Is(err, auth.ErrPasswordPolicy):
		writeError(w, r, 400, api.InvalidRequest, auth.ErrPasswordPolicy.Error())
	case errors.Is(err, auth.ErrOverloaded):
		w.Header().Set("Retry-After", "1")
		writeJSON(w, r, 429, api.ErrorResponse{Error: api.Error{Code: api.Overloaded, Message: "Too many authentication attempts; wait and try again", RetryAfterSeconds: 1}})
	case errors.Is(err, auth.ErrTwoFactorUnavailable):
		writeError(w, r, 503, api.Unavailable, "Two-factor authentication is not available; contact the server operator")
	default:
		logFailure(r, err)
		writeError(w, r, 503, api.Unavailable, "Authentication is temporarily unavailable; try again shortly")
	}
}

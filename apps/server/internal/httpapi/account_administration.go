package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/shared/api"
)

const (
	accountAuditPath  = api.BasePath + "/admin/audit"
	recoveryCodesPath = api.BasePath + "/sessions/recovery-codes"
)

func serveAccountAdministration(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	switch r.URL.Path {
	case accountAuditPath:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, r, 405, api.InvalidRequest, "Use GET")
			return true
		}
		var before int64
		if raw := r.URL.Query().Get("before"); raw != "" {
			var err error
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 1 {
				writeError(w, r, 400, api.InvalidRequest, "Use a valid audit cursor")
				return true
			}
		}
		page, err := service.AccountAudit(r.Context(), bearer(r), before)
		if errors.Is(err, auth.ErrForbidden) {
			writeError(w, r, 403, api.PermissionDenied, "Account audit history requires administrator access")
		} else if err != nil {
			writeAuthenticationError(w, r, err)
		} else {
			writeJSON(w, r, 200, page)
		}
	case recoveryCodesPath:
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, r, 405, api.InvalidRequest, "Use POST")
			return true
		}
		if !sameOrigin(w, r) {
			return true
		}
		var request api.RecoveryCodeReplacement
		if err := decodeSessionBody(w, r, &request); err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		codes, err := service.ReplaceRecoveryCodes(r.Context(), bearer(r), request.Password, request.Code)
		if errors.Is(err, auth.ErrCredentials) {
			writeError(w, r, 401, api.AuthenticationRequired, "Use your account password and a fresh authenticator code. Sign in again if your session expired.")
		} else if err != nil {
			writeAuthenticationError(w, r, err)
		} else {
			writeJSON(w, r, 200, api.RecoveryCodes{Codes: codes})
		}
	default:
		return false
	}
	return true
}

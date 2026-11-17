package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/chironjit/serversend/shared/api"
)

const adminCookie = "__Host-serversend-admin"

// A session-bound CSRF value is only released in no-store same-origin JSON.
// It is not a bearer grant and changes whenever the browser signs in again.
func csrfToken(token string) string {
	sum := sha256.Sum256([]byte("ServerSend browser CSRF v1\x00" + token))
	return hex.EncodeToString(sum[:])
}
func browserProtection(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
		return true
	}
	cookie, err := r.Cookie(adminCookie)
	// Explicit Authorization is a non-ambient credential (including onboarding
	// challenges). A browser cannot attach it through a forged form submission.
	if err != nil || r.Header.Get("Authorization") != "" {
		return true
	}
	if !sameOrigin(w, r) {
		return false
	}
	if r.TLS == nil || r.Header.Get("Origin") != "https://"+r.Host || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrfToken(cookie.Value))) != 1 {
		writeError(w, r, 403, api.PermissionDenied, "Refresh the admin page before making changes")
		return false
	}
	return true
}

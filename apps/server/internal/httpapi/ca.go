package httpapi

import (
	"net/http"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
	"github.com/chironjit/serversend/shared/api"
)

const (
	caInfoPath = api.BasePath + "/admin/ca"
	caFilePath = api.BasePath + "/admin/ca.crt"
)

// WithCA exposes the public private-CA certificate to signed-in administrators.
// It is nil when the deployment uses externally supplied certificates.
func WithCA(certificatePEM []byte) Option {
	return func(o *options) { o.ca = certificatePEM }
}

// serveCA lets an authenticated administrator download the public CA and see
// its fingerprint, to compare with the startup log before trusting it.
func serveCA(w http.ResponseWriter, r *http.Request, accounts *auth.Service, ca []byte) bool {
	if r.URL.Path != caInfoPath && r.URL.Path != caFilePath {
		return false
	}
	session, err := accounts.Authorize(r.Context(), bearer(r))
	if err != nil {
		writeAuthenticationError(w, r, err)
		return true
	}
	if session.Role != "admin" {
		writeAuthenticationError(w, r, auth.ErrForbidden)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, 405, api.InvalidRequest, "Use GET")
		return true
	}
	if len(ca) == 0 {
		writeError(w, r, 404, api.NotFound, "This server uses externally supplied certificates; it has no private certificate authority")
		return true
	}
	if r.URL.Path == caInfoPath {
		writeJSON(w, r, 200, struct {
			SHA256 string `json:"sha256"`
		}{localtls.Fingerprint(ca)})
		return true
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="serversend-ca.crt"`)
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(ca)
	}
	return true
}

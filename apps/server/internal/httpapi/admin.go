package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/chironjit/serversend/apps/server/admin"
	"github.com/chironjit/serversend/shared/api"
)

func adminHandler() http.Handler {
	assets, err := fs.Sub(admin.Files, "dist")
	if err != nil {
		panic(err) // A build-time invariant, not deployment-provided input.
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" {
			http.Redirect(w, r, "/admin/", http.StatusPermanentRedirect)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/admin/")
		if name == "" {
			name = "index.html"
		}
		// Reject aliases and directories rather than exposing listings or falling
		// back to the shell for API, source-map or missing-asset requests.
		if !fs.ValidPath(name) || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") || strings.Contains(name, "\\") {
			writeError(w, r, http.StatusNotFound, api.NotFound, "Endpoint not found")
			return
		}
		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			writeError(w, r, http.StatusNotFound, api.NotFound, "Endpoint not found")
			return
		}
		w.Header().Del("Content-Type")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		http.StripPrefix("/admin", files).ServeHTTP(w, r)
	})
}

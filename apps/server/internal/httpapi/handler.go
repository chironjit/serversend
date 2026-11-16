// Package httpapi serves the public process endpoints. Account and file routes
// remain closed until their authentication and storage services are implemented.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// Capabilities returns provisional resource ceilings, not a buffer allocation
// budget or a claim that upload endpoints are already implemented. HTTP/3 must
// not be advertised until a QUIC listener exists.
func Capabilities() api.Capabilities {
	return api.Capabilities{
		ProtocolVersions: []protocol.Version{protocol.Version1},
		Transports:       []api.Transport{api.HTTP2},
		Limits: protocol.TransferLimits{
			MinChunkBytes: 1 << 20, MaxChunkBytes: 8 << 20,
			MaxRequestBytes: 8 << 20, MaxManifestBytes: 1 << 20, MaxChunks: 1 << 20,
		},
	}
}

// New accepts a bounded dependency check. It must honor context cancellation
// and must never include internal diagnostic details in its public response.
func New(ready func(context.Context) error, settings ...Option) http.Handler {
	var opts options
	for _, setting := range settings {
		setting(&opts)
	}
	admin := adminHandler()
	var transfers *transferRoutes
	if opts.authentication != nil && opts.transfers != nil {
		transfers = newTransferRoutes(opts.authentication, opts.transfers)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if opts.authentication != nil && (serveAuthentication(w, r, opts.authentication) || serveUsers(w, r, opts.authentication)) {
			return
		}
		if transfers != nil && (serveSessions(w, r, opts.authentication) || transfers.serve(w, r)) {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, r, http.StatusMethodNotAllowed, api.InvalidRequest, "Use GET or HEAD for this endpoint")
			return
		}
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			admin.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			writeJSON(w, r, http.StatusOK, struct {
				Status string `json:"status"`
			}{"ok"})
		case "/readyz":
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if ready == nil || ready(ctx) != nil {
				writeError(w, r, http.StatusServiceUnavailable, api.Unavailable, "Server is not ready; try again shortly")
				return
			}
			writeJSON(w, r, http.StatusOK, struct {
				Status string `json:"status"`
			}{"ready"})
		case api.BasePath + "/capabilities":
			writeJSON(w, r, http.StatusOK, Capabilities())
		default:
			writeError(w, r, http.StatusNotFound, api.NotFound, "Endpoint not found")
		}
	})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code api.ErrorCode, message string) {
	writeJSON(w, r, status, api.ErrorResponse{Error: api.Error{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		// All response types contain only values supported by encoding/json.
		_ = json.NewEncoder(w).Encode(value)
	}
}

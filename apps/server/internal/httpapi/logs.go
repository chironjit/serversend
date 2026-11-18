package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/applog"
	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/shared/api"
)

func WithLogs(store *applog.Store) Option { return func(o *options) { o.logs = store } }

type logContextKey struct{}

func logResponseError(r *http.Request, status int, message string) {
	if store, ok := r.Context().Value(logContextKey{}).(*applog.Store); ok {
		level := "warn"
		if status >= 500 {
			level = "error"
		}
		store.Add(level, fmt.Sprintf("HTTP %d: %s", status, message))
	}
}

func logFailure(r *http.Request, err error) {
	if store, ok := r.Context().Value(logContextKey{}).(*applog.Store); ok {
		store.Add("error", fmt.Sprintf("Request failed: %v", err))
	}
}
func serveLogs(w http.ResponseWriter, r *http.Request, service *auth.Service, store *applog.Store) bool {
	if r.URL.Path != api.BasePath+"/admin/logs" {
		return false
	}
	session, err := service.Authorize(r.Context(), bearer(r))
	if err != nil {
		writeAuthenticationError(w, r, err)
		return true
	}
	if session.Role != "admin" {
		writeAuthenticationError(w, r, auth.ErrForbidden)
		return true
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, r, 405, api.InvalidRequest, "Use GET")
		return true
	}
	level, q := r.URL.Query().Get("level"), r.URL.Query().Get("q")
	before := uint64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = strconv.ParseUint(raw, 10, 64)
	}
	if err != nil || len(q) > 256 || (level != "" && level != "info" && level != "warn" && level != "error" && level != "ok") {
		writeError(w, r, 400, api.InvalidRequest, "Use a valid level, search up to 256 bytes and a numeric cursor")
		return true
	}
	if store == nil {
		writeError(w, r, 503, api.Unavailable, "Logging is unavailable")
		return true
	}
	writeJSON(w, r, 200, store.Page(level, q, before))
	return true
}

type logResponse struct {
	http.ResponseWriter
	status int
}

func (w *logResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *logResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *logResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func requestLogs(next http.Handler, store *applog.Store) http.Handler {
	if store == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		response := &logResponse{ResponseWriter: w}
		r = r.WithContext(context.WithValue(r.Context(), logContextKey{}, store))
		defer func() {
			if failure := recover(); failure != nil {
				store.Add("error", "HTTP handler panicked; connection aborted")
				panic(failure)
			}
			status := response.status
			if status == 0 {
				status = 200
			}
			level := "ok"
			if status >= 500 {
				level = "error"
			} else if status >= 400 {
				level = "warn"
			}
			// Never log arbitrary URLs, query strings, headers, bodies or file names.
			route := r.Pattern
			if _, path, ok := strings.Cut(route, " "); ok {
				route = path
			}
			if route == "" {
				switch r.URL.Path {
				case "/healthz", "/readyz", "/admin", "/admin/", passwordPath, passwordReplacePath, totpEnrollPath, totpConfirmPath, sessionsPath, currentSessionPath, accountAuditPath, recoveryCodesPath, api.BasePath + "/admin/users", api.BasePath + "/admin/logs", api.BasePath + "/capabilities":
					route = r.URL.Path
				default:
					route = "unmatched route"
					if strings.HasPrefix(r.URL.Path, "/admin/assets/") {
						route = "admin asset"
					}
				}
			}
			method := r.Method
			switch method {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
			default:
				method = "OTHER"
			}
			store.Add(level, fmt.Sprintf("%s %s — HTTP %d (%d ms)", method, route, status, time.Since(start).Milliseconds()))
		}()
		next.ServeHTTP(response, r)
	})
}

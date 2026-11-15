package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
)

func TestProcessEndpoints(t *testing.T) {
	h := New(func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("readiness check has no bounded deadline")
		}
		return nil
	})
	for _, path := range []string{"/healthz", "/readyz", api.BasePath + "/capabilities"} {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("unexpected response: %v", w)
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatal("HEAD returned a body")
				}
				if method == http.MethodGet && strings.HasSuffix(path, "/capabilities") {
					caps, err := api.DecodeCapabilities(w.Body)
					if err != nil || !caps.SupportsTransport(api.HTTP2) || caps.SupportsTransport(api.HTTP3) {
						t.Fatalf("invalid capabilities: %#v error=%v", caps, err)
					}
				}
			}
		})
	}
}

func TestFailuresUseSafeTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
		code         api.ErrorCode
	}{
		{"GET", "/readyz", 503, api.Unavailable},
		{"GET", "/admin", 404, api.NotFound},
		{"GET", "/api/v1/files", 404, api.NotFound},
		{"POST", "/healthz", 405, api.InvalidRequest},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			h := New(func(context.Context) error { return errors.New("private database failure") })
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if strings.Contains(w.Body.String(), "private") {
				t.Fatal("dependency error leaked")
			}
			errBody, err := api.DecodeError(w.Body)
			if w.Code != tc.status || err != nil || errBody.Code != tc.code {
				t.Fatalf("status=%d body=%#v error=%v", w.Code, errBody, err)
			}
			if tc.status == 405 && w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("method rejection must advertise allowed methods")
			}
		})
	}
	w := httptest.NewRecorder()
	New(nil).ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatal("unconfigured dependency check reported ready")
	}
}

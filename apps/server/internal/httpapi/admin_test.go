package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/admin"
	"github.com/chironjit/serversend/shared/api"
)

func TestEmbeddedAdminShell(t *testing.T) {
	h := New(nil)
	assets, err := fs.Sub(admin.Files, "dist")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(names) < 4 {
		t.Fatal("build the production shell before testing: npm ci && npm run build in apps/server/admin")
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".map") || strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".vue") || strings.Contains(name, "node_modules") {
			t.Fatalf("non-production asset embedded: %s", name)
		}
		if strings.HasSuffix(name, ".css") {
			css, err := fs.ReadFile(assets, name)
			if err != nil {
				t.Fatal(err)
			}
			for _, directive := range []string{"@apply", "@tailwind", "@custom-variant", "@theme"} {
				if strings.Contains(string(css), directive) {
					t.Errorf("uncompiled Tailwind directive %s: install the approved build integration and rebuild the admin shell", directive)
				}
			}
		}
		path := "/admin/" + name
		if name == "index.html" {
			path = "/admin/"
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+path, func(t *testing.T) {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("asset response: %v", w)
				}
				csp := w.Header().Get("Content-Security-Policy")
				for _, directive := range []string{"script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "form-action 'none'", "base-uri 'none'"} {
					if !strings.Contains(csp, directive) {
						t.Fatalf("missing CSP directive %q", directive)
					}
				}
				if strings.Contains(csp, "unsafe-") {
					t.Fatal("shell requires unsafe inline or eval policy")
				}
				wantType := "text/html"
				if strings.HasSuffix(name, ".js") {
					wantType = "javascript"
				} else if strings.HasSuffix(name, ".css") {
					wantType = "text/css"
				} else if strings.HasSuffix(name, ".woff2") {
					wantType = "font/woff2"
				}
				if !strings.Contains(w.Header().Get("Content-Type"), wantType) {
					t.Fatalf("incorrect asset content type: %s", w.Header().Get("Content-Type"))
				}
				if method == http.MethodHead {
					if w.Body.Len() != 0 {
						t.Fatal("HEAD returned an asset body")
					}
				} else {
					want, err := fs.ReadFile(assets, name)
					if err != nil || w.Body.String() != string(want) {
						t.Fatal("response does not match embedded asset")
					}
				}
			})
		}
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	bootstrap := strings.Index(html, `<script src="/admin/theme-init.js"></script>`)
	if bootstrap < 0 || bootstrap > strings.Index(html, `type="module"`) || bootstrap > strings.Index(html, `rel="stylesheet"`) {
		t.Fatal("theme must be applied by a blocking script before the app and styles")
	}
	if !strings.Contains(html, "<noscript") || !strings.Contains(html, `lang="en"`) {
		t.Fatal("shell is missing its language or no-JavaScript explanation")
	}
}

func TestAdminRoutesStayClosedExceptAssets(t *testing.T) {
	h := New(nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/admin?next=https://foreign.invalid", nil))
		if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "/admin/" {
			t.Fatalf("admin redirect must use a fixed local destination: %v", w)
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("redirect HEAD returned a body")
		}
	}
	for _, path := range []string{
		"/", "/administrator", "/admin/assets/", "/admin/unknown", "/admin/.env",
		"/admin/../go.mod", "/admin/%2e%2e/go.mod", "/admin//index.html", "/admin/assets/../index.html",
		"/admin/assets%5c../index.html", "/admin/src/App.vue", "/admin/package.json", "/admin/assets/missing.js",
		"/api/v1/setup", "/api/v1/bootstrap", "/api/v1/users", "/api/v1/files", "/admin/api/v1/users",
	} {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				status := http.StatusNotFound
				if method == http.MethodPost {
					status = http.StatusMethodNotAllowed
				}
				if w.Code != status {
					t.Fatalf("unexpected status: %v", w)
				}
				if _, err := api.DecodeError(w.Body); err != nil {
					t.Fatalf("not a typed error: %v", err)
				}
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/admin/", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("mutation accepted: %v", w)
		}
	}
}

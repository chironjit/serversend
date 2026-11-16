package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
)

// Adapter tests inject a fast deterministic hasher. They test request scopes
// and durable account transitions, not the production password KDF.
type adapterTestHasher struct{}

func (adapterTestHasher) Hash(password string) (string, error) {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:]), nil
}
func (h adapterTestHasher) Verify(hash, password string) bool {
	want, _ := h.Hash(password)
	return want == hash
}

func authenticationHandler(t *testing.T) (http.Handler, auth.IssuedPassword) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service, err := auth.New(database.NewRepository(db), adapterTestHasher{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	return New(nil, WithAuthentication(service)), issued
}

func authRequest(h http.Handler, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPasswordHTTPFlowIsRestricted(t *testing.T) {
	h, issued := authenticationHandler(t)
	body, _ := json.Marshal(api.PasswordAuthentication{Username: "admin", Password: issued.TemporaryPassword})
	w := authRequest(h, api.BasePath+"/auth/password", string(body), "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("password login: %v", w)
	}
	var attempt api.PasswordAttempt
	if err := json.Unmarshal(w.Body.Bytes(), &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.NextStep != "replace_password" || strings.Contains(w.Body.String(), issued.TemporaryPassword) {
		t.Fatal("login granted authority or leaked password")
	}
	for _, path := range []string{api.BasePath + "/files", api.BasePath + "/users", api.BasePath + "/devices", api.BasePath + "/auth/totp/enroll"} {
		w := authRequest(h, path, `{"account_id":"`+string(issued.AccountID)+`"}`, attempt.Challenge)
		if w.Code < 400 {
			t.Fatal("onboarding challenge bypassed scope")
		}
	}
	w = authRequest(h, api.BasePath+"/auth/password/replace", `{"password":"a long permanent password"}`, attempt.Challenge)
	if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("replacement: %v", w)
	}
	w = authRequest(h, api.BasePath+"/auth/password/replace", `{"password":"another long password"}`, attempt.Challenge)
	if w.Code != 401 {
		t.Fatal("replayed replacement accepted")
	}
	w = authRequest(h, api.BasePath+"/auth/password", `{"username":"admin","password":"a long permanent password"}`, "")
	if w.Code != 200 {
		t.Fatal(w)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &attempt); err != nil || attempt.NextStep != "enroll_totp" {
		t.Fatal("password replacement bypassed enrollment")
	}
}

func TestAuthenticationHTTPRejectsMalformedAndCrossOriginRequests(t *testing.T) {
	h, _ := authenticationHandler(t)
	for _, body := range []string{`null`, `{}`, `{"username":"admin","password":"first","Password":"second"}`, `{"username":"admin","password":"pw"}{}`} {
		w := authRequest(h, api.BasePath+"/auth/password", body, "")
		if w.Code != 400 {
			t.Fatalf("malformed body status: %d", w.Code)
		}
		if _, err := api.DecodeError(w.Body); err != nil {
			t.Fatal(err)
		}
	}
	w := authRequest(h, api.BasePath+"/auth/password", strings.Repeat(" ", int(api.MaxAuthenticationBytes)+1), "")
	if w.Code != 413 {
		t.Fatal("oversized authentication body accepted")
	}
	for _, headers := range []map[string]string{
		{"Origin": "https://foreign.invalid"}, {"Sec-Fetch-Site": "cross-site"}, {"Content-Type": "text/plain"}, {"Content-Encoding": "gzip"},
	} {
		r := httptest.NewRequest("POST", "https://server.invalid"+api.BasePath+"/auth/password", strings.NewReader(`{"username":"admin","password":"pw"}`))
		r.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 && w.Code != 415 {
			t.Fatalf("unsafe request accepted: %v", w)
		}
	}
	for _, method := range []string{"GET", "HEAD", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, api.BasePath+"/auth/password", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatal("method boundary ignored")
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("authentication HEAD returned a body")
		}
	}
	w = authRequest(h, api.BasePath+"/auth/password/replace", `{"password":"a long permanent password"}`, "")
	if w.Code != 401 {
		t.Fatal("missing onboarding authorization accepted")
	}
}

package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

func TestBrowserCookieRestorationLogoutAndCSRF(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := auth.New(database.NewRepository(db), adapterTestHasher{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := logTestToken("admin")
	id := "11111111111111111111111111111111"
	if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,'admin','admin','active',1,'hash',0)`, id); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
	if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'browser',?,0,0,?)`, id, id, fmt.Sprintf("%x", sum), time.Now().Add(7*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	handler := New(nil, WithAuthentication(service))
	call := func(method, path, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://server.test"+path, nil)
		r.AddCookie(&http.Cookie{Name: "__Host-serversend-admin", Value: token})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", currentSessionPath, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/api/v1/admin/stats", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("DELETE", currentSessionPath, "https://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin logout accepted")
	}
	w := call("DELETE", currentSessionPath, "https://server.test")
	if w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].MaxAge != -1 {
		t.Fatal("logout cookie not cleared securely")
	}
	if w := call("GET", currentSessionPath, ""); w.Code != 401 {
		t.Fatal("revoked cookie allowed")
	}
}

package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

func TestAdminDevicesRevocationRecoveryAndOrigin(t *testing.T) {
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
	now := time.Now()
	adminID := "11111111111111111111111111111111"
	userID := "22222222222222222222222222222222"
	for _, row := range []struct{ id, role string }{{adminID, "admin"}, {userID, "user"}} {
		if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,?,?,'active',1,'unchanged-hash',?)`, row.id, row.role, row.role, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		token := logTestToken(row.role)
		sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
		if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'Laptop',?,0,?,?)`, row.id, row.id, fmt.Sprintf("%x", sum), now.UnixMilli(), now.Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(nil, WithAuthentication(service))
	prefix := "/api/v1/admin/users/" + userID
	call := func(method, path, role, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://server.test"+path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+logTestToken(role))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", prefix+"/devices", "user", ""); w.Code != 403 {
		t.Fatal("user listed devices", w.Code)
	}
	w := call("GET", prefix+"/devices", "admin", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Laptop") {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("credential metadata leaked")
	}
	if w = call("POST", prefix+"/devices/"+userID+"/revoke", "admin", "https://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin revoke accepted")
	}
	if w = call("POST", prefix+"/devices/"+userID+"/revoke", "admin", "https://server.test"); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if _, err = service.Authorize(t.Context(), logTestToken("user")); err == nil {
		t.Fatal("revoked user token still works")
	}
	if w = call("POST", prefix+"/reset-password", "admin", "https://server.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var issued auth.IssuedPassword
	if err = json.Unmarshal(w.Body.Bytes(), &issued); err != nil || issued.TemporaryPassword == "" {
		t.Fatal("temporary password not issued")
	}
	var enrolled bool
	var recovery string
	if err = db.QueryRow(`SELECT totp_enrolled,recovery_kind FROM accounts WHERE id=?`, userID).Scan(&enrolled, &recovery); err != nil || !enrolled || recovery != "password" {
		t.Fatal("password reset changed other factor", err)
	}
	if w = call("POST", prefix+"/reset-totp", "admin", "https://server.test"); w.Code != 409 {
		t.Fatal("mixed reset accepted", w.Code, w.Body.String())
	}
	// The same reset can be reissued, but an old admin session cannot authorize it.
	if w = call("POST", prefix+"/reset-password", "admin", "https://server.test"); w.Code != 200 {
		t.Fatal("same-kind retry blocked", w.Body.String())
	}
	if _, err = db.Exec(`UPDATE device_sessions SET created_at=? WHERE id=?`, now.Add(-6*time.Minute).UnixMilli(), adminID); err != nil {
		t.Fatal(err)
	}
	if w = call("POST", prefix+"/reset-password", "admin", "https://server.test"); w.Code != 403 {
		t.Fatal("stale administrator reset password")
	}
}

func TestActiveRequestCancelledAfterDeviceRevocation(t *testing.T) {
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
	id := "11111111111111111111111111111111"
	token := logTestToken("user")
	now := time.Now()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,'user','user','active',1,'hash',?)`, id, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
	if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'device',?,0,?,?)`, id, id, fmt.Sprintf("%x", sum), now.UnixMilli(), now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://server.test/api/v1/catalog", nil)
	stop := monitorSession(httptest.NewRecorder(), r, service, token)
	defer stop()
	if _, err = db.Exec(`UPDATE device_sessions SET revoked_at=? WHERE id=?`, now.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("active request survived device revocation")
	}
}

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
	"github.com/chironjit/serversend/shared/api"
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
	if w := call("GET", accountAuditPath, "user", ""); w.Code != 403 {
		t.Fatal("user listed audit events", w.Code)
	}
	if w := call("POST", prefix+"/disable", "user", ""); w.Code != 403 {
		t.Fatal("user disabled account", w.Code)
	}
	if w := call("POST", prefix+"/disable", "admin", "https://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin disable accepted", w.Code)
	}
	if w := call("POST", "/api/v1/admin/users/"+adminID+"/disable", "admin", ""); w.Code != 403 {
		t.Fatal("administrator could disable own account", w.Code)
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
	// A separate live account exercises epoch invalidation on disable and enable.
	const other = "33333333333333333333333333333333"
	if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,'other','user','active',1,'hash',?)`, other, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	otherToken := logTestToken("other")
	sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + otherToken))
	if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'Laptop',?,0,?,?)`, other, other, fmt.Sprintf("%x", sum), now.UnixMilli(), now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	otherPath := "/api/v1/admin/users/" + other
	if w = call("POST", otherPath+"/disable", "admin", ""); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if _, err = service.Authorize(t.Context(), otherToken); err == nil {
		t.Fatal("disabled user still has access")
	}
	for range 2 {
		if w = call("POST", otherPath+"/enable", "admin", ""); w.Code != 204 {
			t.Fatal(w.Body.String())
		}
	}
	if _, err = service.Authorize(t.Context(), otherToken); err == nil {
		t.Fatal("enabling restored the old authorization")
	}
	var page api.AccountAuditPage
	if w = call("GET", accountAuditPath, "admin", ""); w.Code != 200 ||
		json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 5 {
		t.Fatalf("audit page: %d %s", w.Code, w.Body.String())
	}
	if event := page.Events[0]; event.Actor != "admin" || event.Target != "other" || event.Action != "enable" || page.Events[1].Action != "disable" {
		t.Fatalf("missing account changes from audit: %+v", page.Events)
	}
	if _, err = db.Exec(`UPDATE device_sessions SET created_at=? WHERE id=?`, now.Add(-6*time.Minute).UnixMilli(), adminID); err != nil {
		t.Fatal(err)
	}
	if w = call("POST", prefix+"/reset-password", "admin", "https://server.test"); w.Code != 403 {
		t.Fatal("stale administrator reset password")
	}
	if w = call("POST", otherPath+"/disable", "admin", ""); w.Code != 403 {
		t.Fatal("stale administrator disabled account")
	}
	if w = call("GET", accountAuditPath+"?before=invalid", "admin", ""); w.Code != 400 {
		t.Fatal("invalid audit cursor accepted", w.Code)
	}
	for range 47 {
		if _, err = db.Exec(`INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,'disable',?)`, adminID, other, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if w = call("GET", accountAuditPath, "admin", ""); w.Code != 200 ||
		json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 50 || page.NextBefore != page.Events[49].ID {
		t.Fatalf("missing audit cursor: %d %s", w.Code, w.Body.String())
	}
	cursor := page.NextBefore
	page = api.AccountAuditPage{}
	if w = call("GET", fmt.Sprintf("%s?before=%d", accountAuditPath, cursor), "admin", ""); w.Code != 200 ||
		json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 2 || page.NextBefore != 0 || page.Events[0].ID >= cursor {
		t.Fatalf("invalid remaining audit page: %d %s", w.Code, w.Body.String())
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
	request := httptest.NewRequest("POST", "https://server.test/api/v1/sessions/devices/"+id+"/revoke", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	New(nil, WithAuthentication(service)).ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("self-service revocation failed", response.Code, response.Body.String())
	}
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("active request survived device revocation")
	}
}

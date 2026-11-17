package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/applog"
	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

func TestLogAccessAndRedaction(t *testing.T) {
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
	for i, role := range []string{"admin", "user"} {
		id := fmt.Sprintf("%032d", i+1)
		token := logTestToken(role)
		if _, err := db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,?,?,'active',1,'hash',0)`, id, role, role); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
		if _, err := db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'test',?,0,0,?)`, id, id, fmt.Sprintf("%x", sum), time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := applog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	logs.Add("error", "Disk failed")
	h := New(nil, WithAuthentication(service), WithLogs(logs))
	for _, tc := range []struct {
		token, path string
		status      int
	}{
		{"", "/api/v1/admin/logs", 401},
		{logTestToken("user"), "/api/v1/admin/logs", 403},
		{logTestToken("admin"), "/api/v1/admin/logs?level=error&q=disk", 200},
		{logTestToken("admin"), "/api/v1/admin/logs?level=invalid", 400},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 && !strings.Contains(w.Body.String(), "Disk failed") {
			t.Fatal("matching log missing")
		}
		if tc.status != 200 && strings.Contains(w.Body.String(), "Disk failed") {
			t.Fatal("log leak")
		}
	}
	r := httptest.NewRequest("POST", "/secret-name?password=secret-query", strings.NewReader("secret-body"))
	r.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(httptest.NewRecorder(), r)
	for _, e := range logs.Page("", "", 0) {
		if strings.Contains(e.Message, "secret-") {
			t.Fatalf("request secret logged: %s", e.Message)
		}
	}
	if _, err := db.Exec(`UPDATE accounts SET disabled=1 WHERE role='admin'`); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/api/v1/admin/logs", nil)
	r.Header.Set("Authorization", "Bearer "+logTestToken("admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("disabled admin access: %d", w.Code)
	}
}

func logTestToken(role string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(role[:1], 32)))
}

func TestRequestLogDoesNotRepeatMethod(t *testing.T) {
	logs, err := applog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /catalog/{entry}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	requestLogs(mux, logs).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/catalog/private-id", nil))
	page := logs.Page("ok", "", 0)
	if len(page) != 1 || !strings.Contains(page[0].Message, "DELETE /catalog/{entry}") || strings.Contains(page[0].Message, "DELETE DELETE") || strings.Contains(page[0].Message, "private-id") {
		t.Fatalf("unexpected request log: %+v", page)
	}
}

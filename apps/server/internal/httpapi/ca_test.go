package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
)

func TestAdminCADownload(t *testing.T) {
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
	tokens := map[string]string{}
	for i, role := range []string{"admin", "user"} {
		id := fmt.Sprintf("%032d", i+1)
		tokens[role] = logTestToken(role)
		if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,?,?,'active',1,'hash',0)`, id, role, role); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte("ServerSend device access v1\x00" + tokens[role]))
		if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'browser',?,0,0,?)`, id, id, fmt.Sprintf("%x", sum), time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	ca := []byte("-----BEGIN CERTIFICATE-----\nAAECAw==\n-----END CERTIFICATE-----\n")
	call := func(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://server.test"+path, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: adminCookie, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	handler := New(nil, WithAuthentication(service), WithCA(ca))
	w := call(handler, "GET", caFilePath, tokens["admin"])
	if w.Code != 200 || w.Body.String() != string(ca) || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(handler, "GET", caInfoPath, tokens["admin"])
	var info struct{ SHA256 string }
	if err := json.Unmarshal(w.Body.Bytes(), &info); w.Code != 200 || err != nil || info.SHA256 != localtls.Fingerprint(ca) || len(info.SHA256) != 95 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(handler, "GET", caFilePath, ""); w.Code != 401 {
		t.Fatal("anonymous download allowed", w.Code)
	}
	if w = call(handler, "GET", caFilePath, tokens["user"]); w.Code != 403 {
		t.Fatal("user download allowed", w.Code)
	}
	if w = call(handler, "POST", caFilePath, tokens["admin"]); w.Code == 200 {
		t.Fatal("POST accepted")
	}
	if w = call(New(nil, WithAuthentication(service)), "GET", caInfoPath, tokens["admin"]); w.Code != 404 {
		t.Fatal("external certificates reported a private CA", w.Code)
	}
}

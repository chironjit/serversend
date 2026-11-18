package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/apps/server/internal/uploads"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func visibilityFixture(t *testing.T) (http.Handler, *uploads.Service) {
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
	repo := database.NewRepository(db)
	accounts, err := auth.New(repo, adapterTestHasher{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"admin", "user"} {
		id := fmt.Sprintf("%032d", i+1)
		token := logTestToken(role)
		if _, err := db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,?,?,'active',1,'hash',0)`, id, role, role); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte("ServerSend device access v1\x00" + token))
		if _, err := db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at) VALUES(?,?,'test',?,0,0,?)`, id, id, fmt.Sprintf("%x", hash), time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service, err := uploads.New(repo, store, protocol.ID(strings.Repeat("a", 32)), Capabilities().Limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(nil, WithAuthentication(accounts), WithTransfers(service)), service
}
func TestVisibilityAuthorizationAndExpiredCursors(t *testing.T) {
	h, _ := visibilityFixture(t)
	for _, tc := range []struct {
		path, role, cursor string
		status             int
	}{
		{"/statistics", "", "", 401}, {"/admin/statistics", "user", "", 403}, {"/statistics", "admin", "", 403},
		{"/statistics?account_id=00000000000000000000000000000001", "user", "", 200},
		{"/admin/statistics", "admin", "", 200}, {"/admin/events", "user", "", 403},
		{"/events", "user", "broken", 400}, {"/events", "user", "1", 410},
	} {
		r := httptest.NewRequest("GET", api.BasePath+tc.path, nil)
		if tc.role != "" {
			r.Header.Set("Authorization", "Bearer "+logTestToken(tc.role))
		}
		r.Header.Set("Last-Event-ID", tc.cursor)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 && (strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "filesystems\":[") && tc.role == "user") {
			t.Fatal("sensitive/server capacity leaked")
		}
	}
}

type stalledDashboard struct {
	*httptest.ResponseRecorder
	entered, release chan struct{}
	deadline         time.Time
}

func (w *stalledDashboard) SetWriteDeadline(t time.Time) error { w.deadline = t; return nil }
func (w *stalledDashboard) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return 0, errors.New("slow consumer")
}
func TestSlowDashboardNeverBlocksUploadAndEventIsRedacted(t *testing.T) {
	h, service := visibilityFixture(t)
	r := httptest.NewRequest("GET", api.BasePath+"/events", nil)
	r.Header.Set("Authorization", "Bearer "+logTestToken("user"))
	w := &stalledDashboard{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(w, r) }()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _, err := service.Create(ctx, protocol.ID(fmt.Sprintf("%032d", 2)), "slow-dashboard", api.CreateUploadRequest{FileID: protocol.ID(strings.Repeat("b", 32)), VersionID: protocol.ID(strings.Repeat("c", 32)), Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: 1, TransmittedBytes: 1, ManifestBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(w.deadline) > 6*time.Second {
		t.Fatal("unbounded deadline")
	}
	close(w.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}
}

type captureEvent struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *captureEvent) SetWriteDeadline(time.Time) error { return nil }
func (w *captureEvent) Write(p []byte) (int, error) {
	n, e := w.ResponseRecorder.Write(p)
	w.cancel()
	return n, e
}
func TestEventsReplayAndRestartReconciliation(t *testing.T) {
	h, _ := visibilityFixture(t)
	receive := func(cursor string) int64 {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := httptest.NewRequest("GET", api.BasePath+"/events", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+logTestToken("user"))
		r.Header.Set("Last-Event-ID", cursor)
		w := &captureEvent{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
		h.ServeHTTP(w, r)
		var id int64
		for _, line := range strings.Split(w.Body.String(), "\n") {
			if strings.HasPrefix(line, "id: ") {
				id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			}
			if strings.HasPrefix(line, "data: ") {
				var e api.Event
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil || e.Validate() != nil || e.Type != api.StatisticsChanged {
					t.Fatalf("event %s %v", line, err)
				}
			}
		}
		if id == 0 || strings.Contains(w.Body.String(), "account") || strings.Contains(w.Body.String(), "password") {
			t.Fatalf("event: %s", w.Body.String())
		}
		return id
	}
	first := receive("")
	// The next tick is replayable even though no subscriber was connected.
	time.Sleep(2100 * time.Millisecond)
	if next := receive(strconv.FormatInt(first, 10)); next != first+1 {
		t.Fatalf("replay %d -> %d", first, next)
	}
	// A new service epoch rejects a previous instance's cursor before streaming.
	newer, _ := visibilityFixture(t)
	r := httptest.NewRequest("GET", api.BasePath+"/events", nil)
	r.Header.Set("Authorization", "Bearer "+logTestToken("user"))
	r.Header.Set("Last-Event-ID", strconv.FormatInt(first, 10))
	w := httptest.NewRecorder()
	newer.ServeHTTP(w, r)
	if w.Code != 410 || strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("restart: %d %s", w.Code, w.Body.String())
	}
}

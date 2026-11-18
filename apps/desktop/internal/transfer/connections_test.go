package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/zalando/go-keyring"
)

func TestSimultaneousProfilesOfflineIsolationAndScopedControls(t *testing.T) {
	keyring.MockInit()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &Manager{ctx: ctx, emit: func(Item) {}, catalog: map[string]api.CatalogEntry{}}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.enableDurable(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); <-m.durable.closed }()
	clients := make([]*client.Client, 2)
	scopes := make([]protocol.Scope, 2)
	for i := range 2 {
		id := protocol.ID("11111111111111111111111111111111")
		if i == 1 {
			id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
		scopes[i] = protocol.Scope{ServerID: id, AccountID: "22222222222222222222222222222222"}
		session := api.Session{ServerID: id, AccountID: scopes[i].AccountID, Username: "same-user", AccessToken: "in-memory"}
		name := fmt.Sprintf("catalog-%d", i)
		f := &durableUploadFixture{puts: map[int64]int{}}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == api.BasePath+"/sessions" {
				_ = json.NewEncoder(w).Encode(session)
				return
			}
			if r.URL.Path == api.BasePath+"/catalog" {
				_ = json.NewEncoder(w).Encode(api.CatalogPage{Page: api.Page{Snapshot: "55555555555555555555555555555555"}, CatalogRevision: "1", Entries: []api.CatalogEntry{{ID: "33333333333333333333333333333333", Kind: api.File, Mode: protocol.Standard, HeadVersionID: "44444444444444444444444444444444", HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: name}}}})
				return
			}
			f.handler(w, r)
		}))
		srv.EnableHTTP2 = true
		srv.StartTLS()
		defer srv.Close()
		sum := sha256.Sum256(srv.Certificate().Raw)
		clients[i] = durableFixtureClient(t, srv.URL, hex.EncodeToString(sum[:]))
	}
	m.mu.Lock()
	m.clients = map[string]*client.Client{clients[0].Origin(): clients[0], clients[1].Origin(): clients[1]}
	m.client = clients[0]
	m.mu.Unlock()
	for i := range 2 {
		m.mu.Lock()
		m.client = clients[i]
		m.mu.Unlock()
		files, err := m.Files(ctx, "")
		if err != nil || len(files) != 1 || files[0].Name != fmt.Sprintf("catalog-%d", i) {
			t.Fatalf("colliding catalog identifiers crossed servers: %+v %v", files, err)
		}
	}
	m.mu.Lock()
	for i, c := range clients {
		trust := profileTrust()
		trust.Identity.ServerID = scopes[i].ServerID
		m.profiles = append(m.profiles, Profile{Origin: c.Origin(), Trust: trust, AccountID: scopes[i].AccountID})
	}
	m.mu.Unlock()
	for _, c := range clients {
		a, err := m.OpenProfile(ctx, c.Origin())
		if err != nil || a.Username != "same-user" {
			t.Fatal("switch lost live sign-in", err)
		}
	}
	// Keep the first account signed in but make all its requests fail locally.
	clients[0].Close()
	path := filepath.Join(t.TempDir(), "same-name.bin")
	if err := os.WriteFile(path, []byte("isolated contents"), 0600); err != nil {
		t.Fatal(err)
	}
	ids := make([]int, 2)
	for i := range 2 {
		var err error
		ids[i], err = m.durable.store.create(ctx, scopes[i], "same-name", []durableTask{{Kind: "upload", Name: "same-name.bin", Payload: durablePayload{Path: path}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := m.QueueActionScoped(string(scopes[0].ServerID)+"/"+string(scopes[0].AccountID), ids[1], true, "cancel"); err == nil {
		t.Fatal("cross-server control accepted")
	}
	m.durable.notify()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := m.durable.store.items(ctx, scopes[1])
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].State == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthy server blocked by offline profile: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.CombinedTransfers()) != 2 || len(m.CombinedBatches()) != 2 {
		t.Fatal("combined queue omitted an account")
	}
	// Switch selected catalogs while preserving both signed-in clients.
	m.mu.Lock()
	m.client = clients[1]
	m.mu.Unlock()
	if len(m.Transfers()) != 1 || m.Transfers()[0].Scope != string(scopes[1].ServerID)+"/"+string(scopes[1].AccountID) {
		t.Fatal("per-profile queue crossed scopes")
	}
	if err := m.RemoveProfile(clients[0].Origin()); err != nil {
		t.Fatal(err)
	}
	rows, err := m.durable.store.items(ctx, scopes[0])
	if err != nil || len(rows) != 1 {
		t.Fatal("removal deleted queue records", err)
	}
	if len(m.connectedClients()) != 1 {
		t.Fatal("removal disconnected another profile")
	}
}

func TestProfileEditsValidateAndPersistRoutes(t *testing.T) {
	m := &Manager{profilePath: filepath.Join(t.TempDir(), "servers.json"), profiles: []Profile{{Origin: "https://127.0.0.1:58443", Trust: profileTrust()}}}
	for _, routes := range [][]string{{"http://untrusted:80"}, {m.profiles[0].Origin}, {"https://127.0.0.2:58443", "https://127.0.0.2:58443"}} {
		if err := m.EditProfile(m.profiles[0].Origin, "name", routes); err == nil {
			t.Fatal("invalid routes accepted")
		}
	}
	if err := m.EditProfile(m.profiles[0].Origin, "Home", []string{"https://127.0.0.2:58443"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var profiles []Profile
	if json.Unmarshal(raw, &profiles) != nil || profiles[0].Name != "Home" || len(profiles[0].Endpoints) != 1 {
		t.Fatal("profile edit lost")
	}
}

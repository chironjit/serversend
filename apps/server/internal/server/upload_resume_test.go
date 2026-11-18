package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestPersistentUploadResumesAcrossRealServerClientRestartAndRouteChange(t *testing.T) {
	cfg := fixture(t)
	cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSIPs = "", "", "127.0.0.1"
	var requestsMu sync.Mutex
	chunks := map[string]int{}
	admissions, missingQueries := 0, 0
	start := func(address string) (*Server, string, func()) {
		t.Helper()
		server, err := New(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		apiHandler := httpapi.New(server.ready, httpapi.WithIdentity(server.signIdentity), httpapi.WithAuthentication(server.accounts), httpapi.WithTransfers(server.transfers))
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestsMu.Lock()
			if r.Method == "POST" && r.URL.Path == api.BasePath+"/uploads" {
				admissions++
			}
			if r.Method == "PUT" && strings.Contains(r.URL.Path, "/chunks/") {
				chunks[r.URL.Path]++
			}
			if strings.HasSuffix(r.URL.Path, "/missing") {
				missingQueries++
			}
			requestsMu.Unlock()
			apiHandler.ServeHTTP(w, r)
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- server.serve(ctx, listener, handler) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
				if err := server.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return server, listener.Addr().String(), stop
	}
	first, address, stop := start("127.0.0.1:0")
	authority, err := os.ReadFile(filepath.Join(cfg.ConfigDir, localtls.Directory, localtls.CAFile))
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewVerified("https://"+address, client.Trust{AuthorityPEM: string(authority)})
	if err != nil {
		t.Fatal(err)
	}
	c.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	issued, err := first.accounts.BootstrapAdmin(t.Context(), "uploader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.db.Exec(`UPDATE accounts SET role='user' WHERE username='uploader'`); err != nil {
		t.Fatal(err)
	}
	onboardWithClient(t, c, "uploader", issued.TemporaryPassword)
	trust, err := c.VerifiedTrust()
	if err != nil {
		t.Fatal(err)
	}
	source, expected := writeFixture(t, t.TempDir(), "resumed.bin", 9<<20+17)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	save := func(checkpoint *client.UploadCheckpoint) error {
		body, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		return os.WriteFile(checkpointPath, body, 0600)
	}
	load := func() client.UploadCheckpoint {
		t.Helper()
		body, err := os.ReadFile(checkpointPath)
		if err != nil {
			t.Fatal(err)
		}
		var checkpoint client.UploadCheckpoint
		if err = json.Unmarshal(body, &checkpoint); err != nil {
			t.Fatal(err)
		}
		return checkpoint
	}
	var checkpoint client.UploadCheckpoint
	ctx, cancel := context.WithCancel(t.Context())
	_, err = c.UploadResumable(ctx, source, "", &checkpoint, save, func(done, total int64) {
		if done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("upload did not pause: %v", err)
	}
	paused := load()
	if paused.UploadID == "" || paused.AcknowledgedBytes != 4<<20 || paused.Result != nil {
		t.Fatalf("invalid paused checkpoint: ID=%s bytes=%d", paused.UploadID, paused.AcknowledgedBytes)
	}
	before, err := c.Catalog(t.Context())
	if err != nil || len(before.Entries) != 0 {
		t.Fatalf("partial upload became visible: %+v %v", before, err)
	}
	credentials := c.Credentials()
	c.Close()
	stop()
	second, alternateAddress, stopSecond := start("127.0.0.1:0")
	restored, err := client.NewVerifiedRoutes("https://"+address, []string{"https://" + alternateAddress}, trust)
	if err != nil {
		t.Fatal(err)
	}
	restored.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	// This is the remembered-credential restore boundary used by the desktop;
	// rotation is checkpointed independently of the local upload metadata.
	restored.SetCredentialStore(func(state client.CredentialState) error { credentials = state; return nil })
	if err = restored.RestoreCredentials(t.Context(), credentials); err != nil {
		t.Fatalf("restore remembered authorization: %v", err)
	}
	result, err := restored.UploadResumable(t.Context(), source, "", &paused, save, nil)
	if err != nil {
		t.Fatalf("resume after real server restart: %v", err)
	}
	if result.UploadID != checkpoint.UploadID || result.FileID != checkpoint.Manifest.FileID {
		t.Fatal("resume replaced the immutable upload identity")
	}
	var storedChunks int
	if err = second.db.QueryRow(`SELECT count(*) FROM chunks WHERE upload_id=?`, result.UploadID).Scan(&storedChunks); err != nil || storedChunks != 3 {
		t.Fatalf("durable chunk metadata: %d %v", storedChunks, err)
	}
	// Simulate a committed finalization whose local completion checkpoint was
	// lost. Another real server restart must return that result even if the
	// original source has disappeared, without publishing another version.
	completed := load()
	completed.Result = nil
	if err = save(&completed); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	credentials = restored.Credentials()
	restored.Close()
	stopSecond()
	third, _, _ := start(address)
	replay, err := client.NewVerified("https://"+address, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	replay.SetInstallationID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	replay.SetCredentialStore(func(state client.CredentialState) error { credentials = state; return nil })
	if err = replay.RestoreCredentials(t.Context(), credentials); err != nil {
		t.Fatal(err)
	}
	interruptedFinalize := load()
	replayed, err := replay.UploadResumable(t.Context(), source, "", &interruptedFinalize, save, nil)
	if err != nil || replayed != result {
		t.Fatalf("committed result did not survive restart: %+v %v", replayed, err)
	}
	page, err := replay.Catalog(t.Context())
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != result.FileID {
		t.Fatalf("catalog duplicated publication: %+v %v", page, err)
	}
	destinationRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(destinationRoot, "restored.bin")
	if err = replay.Download(t.Context(), page.Entries[0], destination, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("restored bytes differ: %v", err)
	}
	account, _ := replay.Account()
	var versions int
	if err = third.db.QueryRow(`SELECT count(*) FROM versions WHERE account_id=?`, account.AccountID).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("duplicate immutable versions: %d %v", versions, err)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if admissions != 1 || len(chunks) != 3 || missingQueries < 2 {
		t.Fatalf("resume repeated admission or omitted reconciliation: admissions=%d chunks=%v missing=%d", admissions, chunks, missingQueries)
	}
	for route, count := range chunks {
		if count != 1 {
			t.Fatalf("acknowledged chunk resent: %s count=%d", route, count)
		}
	}
	if interruptedFinalize.Result == nil || interruptedFinalize.Result.State != protocol.UploadCommitted {
		t.Fatal("replayed result was not checkpointed")
	}
}

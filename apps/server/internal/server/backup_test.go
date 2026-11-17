package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/backup"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestOfflineBackupRestoresStandardBytesAndIdentity(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	originalID := s.Identity()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	stopped := false
	defer func() {
		cancel()
		if !stopped {
			<-done
		}
	}()
	probe, err := client.Probe(t.Context(), "https://"+address)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := onboardedClient(t, probe.Origin, probe.Fingerprint, "admin", issued.TemporaryPassword)
	user, err := admin.CreateUser(t.Context(), "backup-user")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second) // Allow the real authentication limiter to refill.
	uploader, _ := onboardedClient(t, probe.Origin, probe.Fingerprint, "backup-user", user.TemporaryPassword)
	_, expected := writeFixture(t, t.TempDir(), "recover.bin", (4<<20)+17)
	account, _ := uploader.Account()
	// Seed a standard version through the production storage service. This
	// isolates the backup boundary from desktop source-scanner implementation.
	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1,
		Scope:  protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID},
		FileID: "11111111111111111111111111111111", VersionID: "22222222222222222222222222222222",
		Mode: protocol.Standard, TotalBytes: int64(len(expected)), Standard: &protocol.StandardMetadata{Name: "recover.bin"}}
	for offset := 0; offset < len(expected); offset += 4 << 20 {
		data := expected[offset:min(offset+4<<20, len(expected))]
		digest := sha256.Sum256(data)
		manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(len(manifest.Chunks)), Bytes: int64(len(data)), SHA256: protocol.Digest(hex.EncodeToString(digest[:]))})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	intent := api.CreateUploadRequest{FileID: manifest.FileID, VersionID: manifest.VersionID, Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: int64(len(manifest.Chunks)), TransmittedBytes: manifest.TotalBytes, ManifestBytes: int64(len(body))}
	upload, _, err := s.transfers.Create(t.Context(), account.AccountID, "backup-fixture", intent)
	if err != nil {
		t.Fatal(err)
	}
	for i, chunk := range manifest.Chunks {
		offset := i * (4 << 20)
		if _, _, err := s.transfers.PutChunk(t.Context(), account.AccountID, upload.ID, chunk.Index, chunk.Bytes, chunk.SHA256, bytes.NewReader(expected[offset:offset+int(chunk.Bytes)])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.transfers.Finalize(t.Context(), account.AccountID, upload.ID, body); err != nil {
		t.Fatal(err)
	}
	// The backup barrier is a stopped process, including stopped GC/readers.
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stopped = true
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := backup.Create(t.Context(), c, destination); err != nil {
		t.Fatal(err)
	}
	// Use independent temporary volumes and the preserved TLS certificate.
	// The original instance remains stopped: this is a replacement, not a clone.
	restoredConfig := c
	restoredConfig.ConfigDir = filepath.Join(destination, "config")
	restoredConfig.DataDir = filepath.Join(destination, "data")
	restoredConfig.StagingDir = filepath.Join(destination, "staging")
	restoredConfig.TLSCertFile = filepath.Join(destination, "external-tls", "server.crt")
	restoredConfig.TLSKeyFile = filepath.Join(destination, "external-tls", "server.key")
	restored, err := New(t.Context(), restoredConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Identity() != originalID {
		t.Fatal("restore changed server identity")
	}
	uploader.Close() // Drop the old TLS connection but retain its server session.
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	restoreCtx, stopRestore := context.WithCancel(t.Context())
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- restored.Serve(restoreCtx, listener) }()
	defer func() {
		stopRestore()
		if err := <-restoreDone; err != nil {
			t.Error(err)
		}
	}()
	page, err := uploader.Catalog(t.Context())
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("account/session/catalog not restored: %+v %v", page, err)
	}
	recovered := filepath.Join(t.TempDir(), "recovered.bin")
	if err := uploader.Download(t.Context(), page.Entries[0], recovered, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(recovered)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatal("restored standard content differs:", err)
	}
}

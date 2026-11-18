package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/backup"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateRecoveryResumptionAndCanaries(t *testing.T) {
	cfg := fixture(t)
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	probe, err := client.Probe(t.Context(), "https://"+listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := onboardedClient(t, probe.Origin, probe.Fingerprint, "admin", issued.TemporaryPassword)
	user, err := admin.CreateUser(t.Context(), "private-user")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := onboardedClient(t, probe.Origin, probe.Fingerprint, "private-user", user.TemporaryPassword)
	defer c.Close()
	account, _ := c.Account()
	scope := protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}
	e, r, k, err := vault.Create(scope, "private vault passphrase canary")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.PutVault(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err = c.PutVault(t.Context(), e); err == nil {
		t.Fatal("creation race accepted")
	}
	if err = c.SetVaultKey(k, time.Hour); err != nil {
		t.Fatal(err)
	}
	folder, err := c.CreatePrivateDirectory(t.Context(), "", "secret-folder-canary")
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.CreatePrivateDirectory(t.Context(), "", "secret-folder-canary")
	if err != nil || again.ID != folder.ID {
		t.Fatal("private folder merge", err)
	}
	source := filepath.Join(t.TempDir(), "secret-name-canary.txt")
	plain := bytes.Repeat([]byte("PRIVATE-CONTENT-CANARY"), 450000)
	if err = os.WriteFile(source, plain, 0600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(t.TempDir(), "stage")
	var cp client.UploadCheckpoint
	var checkpoint []byte
	interrupted := false
	save := func(v *client.UploadCheckpoint) error {
		checkpoint, _ = json.Marshal(v)
		if v.AcknowledgedBytes > 0 && !interrupted {
			interrupted = true
			return errors.New("checkpoint interruption")
		}
		return nil
	}
	if _, err = c.UploadPrivateResumable(t.Context(), source, stage, folder.ID, &cp, save, nil); err == nil {
		t.Fatal("expected interruption")
	}
	var resumed client.UploadCheckpoint
	if err = json.Unmarshal(checkpoint, &resumed); err != nil {
		t.Fatal(err)
	}
	result, err := c.UploadPrivateResumable(t.Context(), source, stage, folder.ID, &resumed, func(v *client.UploadCheckpoint) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Completed publication is safe to replay after a missing final response.
	resumed.Result = nil
	if _, err = c.UploadPrivateResumable(t.Context(), source, stage, folder.ID, &resumed, func(v *client.UploadCheckpoint) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	// Corrupt an unacknowledged stage tail: cancel the old upload before a
	// fresh version is encrypted, then finish from the unchanged source.
	repairPath := filepath.Join(t.TempDir(), "private-repair.txt")
	if err = os.WriteFile(repairPath, plain[:5<<20], 0600); err != nil {
		t.Fatal(err)
	}
	repairStage := filepath.Join(t.TempDir(), "repair-stage")
	var repair client.UploadCheckpoint
	stoppedRepair := false
	repairSave := func(v *client.UploadCheckpoint) error {
		if v.AcknowledgedBytes > 0 && !stoppedRepair {
			stoppedRepair = true
			return errors.New("interrupt repair fixture")
		}
		return nil
	}
	if _, err = c.UploadPrivateResumable(t.Context(), repairPath, repairStage, "", &repair, repairSave, nil); err == nil {
		t.Fatal("repair interruption")
	}
	previousVersion := repair.Manifest.VersionID
	stageFile, err := os.OpenFile(repairStage, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	var tail [1]byte
	if _, err = stageFile.ReadAt(tail[:], repair.Manifest.TotalBytes-1); err != nil {
		t.Fatal(err)
	}
	tail[0] ^= 1
	if _, err = stageFile.WriteAt(tail[:], repair.Manifest.TotalBytes-1); err != nil {
		t.Fatal(err)
	}
	stageFile.Close()
	if _, err = c.UploadPrivateResumable(t.Context(), repairPath, repairStage, "", &repair, repairSave, nil); !errors.Is(err, client.ErrSourceChanged) {
		t.Fatal("corrupt stage was not retired", err)
	}
	if repair.Manifest.FileID != "" {
		t.Fatal("old private identity reused")
	}
	if _, err = c.UploadPrivateResumable(t.Context(), repairPath, repairStage, "", &repair, repairSave, nil); err != nil {
		t.Fatal(err)
	}
	if repair.Manifest.VersionID == previousVersion {
		t.Fatal("fresh attempt reused private version")
	}
	c.LockVault()
	if _, err = c.VaultKey(); !errors.Is(err, vault.ErrLocked) {
		t.Fatal(err)
	}
	// New client has only account access and exported recovery material.
	fresh, err := client.New(probe.Origin, probe.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.RestoreSession(t.Context(), c.SavedSession()); err != nil {
		t.Fatal(err)
	}
	remote, err := fresh.Vault(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := remote.Recover(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.SetVaultKey(recovered, time.Hour); err != nil {
		t.Fatal(err)
	}
	page, err := fresh.CatalogFolder(t.Context(), folder.ID)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("catalog %v %v", page, err)
	}
	entry := page.Entries[0]
	if entry.Standard != nil || entry.Private == nil {
		t.Fatal("plaintext catalog")
	}
	name, err := fresh.PrivateName(t.Context(), entry)
	if err != nil || name != filepath.Base(source) {
		t.Fatal(name, err)
	}
	dest := filepath.Join(t.TempDir(), "recovered")
	if err = fresh.Download(t.Context(), entry, dest, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(actual, plain) {
		t.Fatal("recovery mismatch", err)
	}
	matches, err := fresh.DownloadMatches(t.Context(), entry, dest)
	if err != nil || !matches {
		t.Fatal("private publication acknowledgement", err)
	}
	statistics, err := fresh.StorageStatistics(t.Context())
	if err != nil || statistics.LogicalBytes != 0 || statistics.PrivateCiphertextBytes <= int64(len(plain)) {
		t.Fatal("private overhead accounting", err)
	}
	history, err := fresh.History(t.Context(), entry.ID, "")
	if err != nil || len(history.Versions) != 1 || history.Versions[0].Mode != protocol.Private {
		t.Fatal("private history", err)
	}
	next, err := remote.Rewrap(recovered, "new vault passphrase canary")
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.PutVault(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err = fresh.PutVault(t.Context(), next); err == nil {
		t.Fatal("stale rewrap accepted")
	}
	if result.FileID != entry.ID {
		t.Fatal("identity mismatch")
	}
	for _, root := range []string{cfg.ConfigDir, cfg.DataDir, cfg.StagingDir} {
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			for _, canary := range [][]byte{[]byte("secret-name-canary"), []byte("secret-folder-canary"), []byte("PRIVATE-CONTENT-CANARY"), []byte("private vault passphrase canary"), k[:], r.Secret[:]} {
				if bytes.Contains(raw, canary) || bytes.Contains([]byte(path), canary) {
					t.Errorf("private canary leaked at %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Stopped-server backup preserves wrappers, ciphertext, history and identity.
	packagePath := filepath.Join(t.TempDir(), "recovery.json")
	raw, _ := json.Marshal(r)
	if err = os.WriteFile(packagePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stopped = true
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if err = backup.Create(t.Context(), cfg, snapshot); err != nil {
		t.Fatal(err)
	}
	restoredCfg := cfg
	restoredCfg.ConfigDir = filepath.Join(snapshot, "config")
	restoredCfg.DataDir = filepath.Join(snapshot, "data")
	restoredCfg.StagingDir = filepath.Join(snapshot, "staging")
	restoredCfg.TLSCertFile = filepath.Join(snapshot, "external-tls", "server.crt")
	restoredCfg.TLSKeyFile = filepath.Join(snapshot, "external-tls", "server.key")
	restored, err := New(t.Context(), restoredCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Identity() != s.Identity() {
		t.Fatal("identity changed")
	}
	listener, err = net.Listen("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	restoreCtx, stopRestore := context.WithCancel(t.Context())
	restoredDone := make(chan error, 1)
	go func() { restoredDone <- restored.Serve(restoreCtx, listener) }()
	defer func() { stopRestore(); <-restoredDone }()
	clean, err := client.New(probe.Origin, probe.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer clean.Close()
	if err = clean.RestoreSession(t.Context(), fresh.SavedSession()); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	var exported vault.Recovery
	if err = json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	envelope, err := clean.Vault(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	key, err := envelope.Recover(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err = clean.SetVaultKey(key, time.Hour); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "after-backup")
	if err = clean.Download(t.Context(), entry, destination, nil); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(contents, plain) {
		t.Fatal("backup recovery mismatch", err)
	}
}

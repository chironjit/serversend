package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/instancelock"
	"github.com/chironjit/serversend/shared/sqlite"
)

func deployment(t *testing.T) config.Config {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows durable directory flushing remains a native qualification gate")
	}
	root := t.TempDir()
	c := config.Defaults()
	c.ConfigDir, c.DataDir, c.StagingDir = filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "staging")
	c.TLSIPs = "127.0.0.1"
	for _, path := range []string{c.ConfigDir, c.DataDir, c.StagingDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func metadata(t *testing.T, c config.Config) *sql.DB {
	t.Helper()
	db, err := database.Open(t.Context(), c.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func write(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete backup or excluded file exists: %s (%v)", path, err)
	}
}

func TestCreateIncludesCommittedWALKeysObjectsAndStaging(t *testing.T) {
	c := deployment(t)
	db := metadata(t, c)
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE); PRAGMA wal_autocheckpoint=0; CREATE TABLE backup_canary(value TEXT); INSERT INTO backup_canary VALUES ('committed WAL: 東京');"); err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(filepath.Join(c.ConfigDir, database.Filename+"-wal"))
	if err != nil || wal.Size() == 0 {
		t.Fatalf("fixture needs committed WAL: %v", err)
	}
	files := map[string][]byte{
		"config/identity.json":   []byte("persistent server identity"),
		"config/tls/ca.key":      []byte("private CA key"),
		"config/totp.key":        []byte("TOTP encryption key"),
		"data/objects/a/blob":    bytes.Repeat([]byte{0, 1, 2, 255}, 70_000),
		"staging/upload/partial": []byte("partially uploaded bytes"),
	}
	for relative, contents := range files {
		write(t, filepath.Join(filepath.Dir(c.ConfigDir), relative), contents)
	}
	if err := os.Mkdir(filepath.Join(c.DataDir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := Create(t.Context(), c, destination); err != nil {
		t.Fatal(err)
	}
	for relative, expected := range files {
		path := filepath.Join(destination, relative)
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("backup bytes differ for %s: %v", relative, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("unprotected file %s: %v", relative, err)
		}
	}
	for _, relative := range []string{".", "config", "data", "data/empty", "staging"} {
		info, err := os.Stat(filepath.Join(destination, relative))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("missing/unprotected directory %s: %v", relative, err)
		}
	}
	for _, volume := range []string{"config", "data", "staging"} {
		absent(t, filepath.Join(destination, volume, instancelock.Filename))
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		absent(t, filepath.Join(destination, "config", database.Filename+suffix))
	}
	var manifest Manifest
	contents, err := os.ReadFile(filepath.Join(destination, "backup.json"))
	if err != nil || json.Unmarshal(contents, &manifest) != nil || manifest.Version != 1 || manifest.TLSIPs != c.TLSIPs || manifest.ExternalTLS || manifest.CreatedAt.IsZero() {
		t.Fatalf("invalid manifest: %s (%v)", contents, err)
	}
	snapshot, err := sqlite.OpenPrivate(t.Context(), filepath.Join(destination, "config", database.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var payload, integrity string
	if err := snapshot.QueryRow("SELECT value FROM backup_canary").Scan(&payload); err != nil || payload != "committed WAL: 東京" {
		t.Fatalf("WAL missing: %q (%v)", payload, err)
	}
	if err := snapshot.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("inconsistent snapshot: %q (%v)", integrity, err)
	}
}

func TestCreateRefusesOverwriteAndUnsafeDestinations(t *testing.T) {
	c := deployment(t)
	metadata(t, c)
	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(existing, "keep"), []byte("untouched"))
	if err := Create(t.Context(), c, existing); err == nil {
		t.Fatal("overwrote existing backup")
	}
	if err := Create(t.Context(), c, existing+string(os.PathSeparator)); err == nil {
		t.Fatal("accepted existing destination with trailing separator")
	}
	contents, err := os.ReadFile(filepath.Join(existing, "keep"))
	if err != nil || string(contents) != "untouched" {
		t.Fatal("existing destination changed", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	write(t, file, []byte("untouched"))
	for _, destination := range []string{file, "relative-backup", filepath.Join(t.TempDir(), "missing", "snapshot"), filepath.Join(c.ConfigDir, "snapshot"), filepath.Join(c.DataDir, "snapshot"), filepath.Join(c.StagingDir, "snapshot")} {
		if err := Create(t.Context(), c, destination); err == nil {
			t.Fatalf("accepted unsafe destination %s", destination)
		}
	}
	contents, err = os.ReadFile(file)
	if err != nil || string(contents) != "untouched" {
		t.Fatal("existing destination file changed", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(c.DataDir, alias); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if err := Create(t.Context(), c, filepath.Join(alias, "snapshot")); err == nil {
		t.Fatal("accepted nested destination behind symlink")
	}
	absent(t, filepath.Join(c.DataDir, "snapshot"))
}

func TestCreateRefusesActiveDeploymentAndReleasesLocks(t *testing.T) {
	for _, volume := range []string{"config", "data", "staging"} {
		t.Run(volume, func(t *testing.T) {
			c := deployment(t)
			metadata(t, c)
			held, err := instancelock.Acquire(filepath.Join(filepath.Dir(c.ConfigDir), volume))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			destination := filepath.Join(t.TempDir(), "snapshot")
			if err := Create(t.Context(), c, destination); err == nil || !strings.Contains(err.Error(), "stop the server") {
				t.Fatalf("active deployment accepted: %v", err)
			}
			absent(t, destination)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err := Create(t.Context(), c, destination); err != nil {
				t.Fatal("failed backup leaked locks:", err)
			}
		})
	}
}

// Cancel after an output file exists, independent of disk speed and timers.
type cancelDuringCopy struct {
	context.Context
	path   string
	cancel context.CancelFunc
}

func (c cancelDuringCopy) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCreateCancellationAndCorruptionRemoveIncompleteDestinations(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		c := deployment(t)
		metadata(t, c)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		destination := filepath.Join(t.TempDir(), "snapshot")
		if err := Create(ctx, c, destination); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation not preserved:", err)
		}
		absent(t, destination)
	})
	t.Run("interrupted copy", func(t *testing.T) {
		c := deployment(t)
		metadata(t, c)
		write(t, filepath.Join(c.DataDir, "blob"), bytes.Repeat([]byte("content"), 50_000))
		destination := filepath.Join(t.TempDir(), "snapshot")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		interrupted := cancelDuringCopy{Context: ctx, path: filepath.Join(destination, "data", "blob"), cancel: cancel}
		if err := Create(interrupted, c, destination); !errors.Is(err, context.Canceled) {
			t.Fatal("mid-copy cancellation not preserved:", err)
		}
		absent(t, destination)
		locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
		if err != nil {
			t.Fatal("cancelled backup leaked locks:", err)
		}
		locks.Close()
	})
	t.Run("corrupt metadata", func(t *testing.T) {
		c := deployment(t)
		write(t, filepath.Join(c.ConfigDir, database.Filename), []byte("not SQLite"))
		destination := filepath.Join(t.TempDir(), "snapshot")
		if err := Create(t.Context(), c, destination); err == nil {
			t.Fatal("corruption reported a complete backup")
		}
		absent(t, destination)
	})
	t.Run("uninitialized deployment", func(t *testing.T) {
		c := deployment(t)
		destination := filepath.Join(t.TempDir(), "snapshot")
		if err := Create(t.Context(), c, destination); err == nil {
			t.Fatal("uninitialized deployment reported a complete backup")
		}
		absent(t, destination)
		absent(t, filepath.Join(c.ConfigDir, database.Filename))
	})
}

func TestCreateRefusesLinksWithoutCopyingTheirTargets(t *testing.T) {
	for _, kind := range []string{"file", "directory", "broken"} {
		t.Run(kind, func(t *testing.T) {
			c := deployment(t)
			metadata(t, c)
			target := filepath.Join(t.TempDir(), "canary")
			if kind == "directory" {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(target, "secret"), []byte("outside deployment"))
			} else if kind == "file" {
				write(t, target, []byte("outside deployment"))
			}
			if err := os.Symlink(target, filepath.Join(c.DataDir, "link")); err != nil {
				t.Skipf("symlink fixture unavailable: %v", err)
			}
			destination := filepath.Join(t.TempDir(), "snapshot")
			if err := Create(t.Context(), c, destination); err == nil || !strings.Contains(err.Error(), "refuses links") {
				t.Fatalf("link accepted: %v", err)
			}
			absent(t, destination)
		})
	}
}

func TestCreateIncludesExternalTLSMaterial(t *testing.T) {
	c := deployment(t)
	metadata(t, c)
	c.TLSIPs = ""
	c.TLSCertFile, c.TLSKeyFile = filepath.Join(t.TempDir(), "external.crt"), filepath.Join(t.TempDir(), "external.key")
	write(t, c.TLSCertFile, []byte("operator certificate"))
	write(t, c.TLSKeyFile, []byte("operator private key"))
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := Create(t.Context(), c, destination); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"server.crt": "operator certificate", "server.key": "operator private key"} {
		contents, err := os.ReadFile(filepath.Join(destination, "external-tls", name))
		if err != nil || string(contents) != expected {
			t.Fatalf("external TLS bytes missing for %s: %v", name, err)
		}
	}
	contents, err := os.ReadFile(filepath.Join(destination, "backup.json"))
	var manifest Manifest
	if err != nil || json.Unmarshal(contents, &manifest) != nil || !manifest.ExternalTLS || manifest.TLSIPs != "" {
		t.Fatalf("incorrect external TLS manifest: %s (%v)", contents, err)
	}
}

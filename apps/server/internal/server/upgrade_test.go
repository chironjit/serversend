package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/auth/twofactor"
	"github.com/chironjit/serversend/apps/server/internal/backup"
	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/sqlite"
)

// installSchema13 writes the shipped schema-13 fixture as the deployment's
// metadata, standing in for a database created by the previous release.
func installSchema13(t *testing.T, c config.Config) {
	t.Helper()
	if _, err := twofactor.LoadProtectionKey(c.ConfigDir, true); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.OpenPrivate(t.Context(), filepath.Join(c.ConfigDir, database.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"v12.sql", "v13.sql"} {
		fixture, err := os.ReadFile(filepath.Join("..", "database", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(fixture)); err != nil {
			t.Fatal(err)
		}
	}
}

func schemaOf(t *testing.T, dir string) (int, string) {
	t.Helper()
	version, id, err := database.Installed(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return version, id
}

func TestUpgradeRequiresBackupAndDowngradeRestoresPriorSchema(t *testing.T) {
	c := fixture(t)
	installSchema13(t, c)
	_, id := schemaOf(t, c.ConfigDir)

	if _, err := New(t.Context(), c); err == nil || !strings.Contains(err.Error(), "SERVER_SEND_UPGRADE_BACKUP_DIR") {
		t.Fatal("upgrade without a backup policy was not refused:", err)
	}
	if version, _ := schemaOf(t, c.ConfigDir); version != 13 {
		t.Fatal("refused upgrade migrated metadata:", version)
	}

	c.UpgradeBackupDir = t.TempDir()
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if version, _ := schemaOf(t, c.ConfigDir); version != database.LatestVersion() {
		t.Fatal("upgrade did not migrate:", version)
	}
	entries, err := os.ReadDir(c.UpgradeBackupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one pre-upgrade backup: %v %v", entries, err)
	}
	snapshot := filepath.Join(c.UpgradeBackupDir, entries[0].Name())
	data, err := os.ReadFile(filepath.Join(snapshot, "backup.json"))
	var manifest backup.Manifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 13 || manifest.ServerID != id {
		t.Fatalf("pre-upgrade manifest: %s %v", data, err)
	}

	// A current-schema restart takes no further backup.
	s, err = New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(c.UpgradeBackupDir); len(entries) != 1 {
		t.Fatal("restart without migration created another backup")
	}

	// Downgrade: restore the matching backup as a replacement, untouched.
	restored := fixture(t)
	restored.UpgradeBackupDir = c.UpgradeBackupDir
	if got, err := backup.Restore(t.Context(), restored, snapshot, backup.Replace); err != nil || got != id {
		t.Fatal(got, err)
	}
	if version, _ := schemaOf(t, restored.ConfigDir); version != 13 {
		t.Fatal("replacement restore changed the schema:", version)
	}
	// Re-upgrading the rolled-back deployment must not overwrite its backup.
	if _, err := New(t.Context(), restored); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal("existing pre-upgrade backup was not protected:", err)
	}
	restored.UpgradeBackupDir = config.SkipUpgradeBackup
	s, err = New(t.Context(), restored)
	if err != nil {
		t.Fatal("explicit opt-out did not upgrade:", err)
	}
	if s.Identity().ID != id {
		t.Fatal("replacement changed identity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

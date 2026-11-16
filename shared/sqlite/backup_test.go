package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCommittedWALAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := openTestDB(t, filepath.Join(dir, "source.db"))
	if _, err := source.Exec(`PRAGMA wal_autocheckpoint=0; CREATE TABLE records(value TEXT); INSERT INTO records VALUES('before')`); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "snapshot.db")
	if err := Backup(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO records VALUES('after')`); err != nil {
		t.Fatal(err)
	}
	restored := openTestDB(t, target)
	var value, check string
	if err := restored.QueryRow("SELECT group_concat(value) FROM records").Scan(&value); err != nil || value != "before" {
		t.Fatalf("snapshot=%q error=%v", value, err)
	}
	if err := restored.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity=%s error=%v", check, err)
	}
	restored.Close()
	if err := Backup(ctx, source, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing backup overwritten: %v", err)
	}
	if err := Backup(ctx, source, filepath.Join(dir, "missing", "snapshot.db")); err == nil {
		t.Fatal("missing directory accepted")
	}
	if err := Backup(ctx, source, "relative.db"); err == nil {
		t.Fatal("relative destination accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	filename := filepath.Join(dir, "cancel.db")
	if err := Backup(canceled, source, filename); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel left destination")
	}
	source.Close()
	if err := Backup(ctx, source, filename); err == nil {
		t.Fatal("closed source accepted")
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failure left destination")
	}
	if err := Backup(ctx, nil, filename); err == nil {
		t.Fatal("nil source accepted")
	}
}

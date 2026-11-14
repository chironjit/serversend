package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func openTestDB(t *testing.T, filename string) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenPolicyAndPersistence(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "metadata.db")
	db := openTestDB(t, filename)
	for pragma, want := range map[string]string{
		"journal_mode": "wal", "foreign_keys": "1", "synchronous": "2", "cache_size": "-2048", "busy_timeout": "5000",
	} {
		var got string
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	if db.Stats().MaxOpenConnections != 1 {
		t.Fatal("connection pool is not bounded to one connection")
	}
	if _, err := db.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY, value TEXT NOT NULL); INSERT INTO item(value) VALUES ('persisted')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestDB(t, filename)
	var value string
	if err := reopened.QueryRow("SELECT value FROM item").Scan(&value); err != nil || value != "persisted" {
		t.Fatalf("persisted value=%q error=%v", value, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filename)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("database has unsafe permissions: %v, %v", info, err)
		}
	}
}

func TestOpenRejectsInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	notDirectory := filepath.Join(dir, "file")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ":memory:", "file::memory:?cache=shared", "relative.db", filepath.Join(dir, "missing", "data.db"), filepath.Join(notDirectory, "data.db"), dir, filepath.Join(dir, "bad\x00name")} {
		t.Run(name, func(t *testing.T) {
			if db, err := Open(context.Background(), name); err == nil || db != nil {
				if db != nil {
					db.Close()
				}
				t.Fatalf("invalid path accepted: %q", name)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent was created: %v", err)
	}
}

func TestReplacementConnectionKeepsPolicy(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "replace.db"))
	db.SetMaxIdleConns(0)
	for attempt := 0; attempt < 2; attempt++ {
		var foreignKeys int
		if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
			t.Fatalf("replacement lost foreign-key policy: %d, %v", foreignKeys, err)
		}
	}
}

func TestOpenLiteralFilename(t *testing.T) {
	name := "metadata # % &.db"
	if runtime.GOOS != "windows" {
		name = "metadata # % ?mode=memory&cache=shared.db"
	}
	filename := filepath.Join(t.TempDir(), name)
	db := openTestDB(t, filename)
	if _, err := db.Exec("CREATE TABLE literal_path(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db = openTestDB(t, filename)
	if _, err := db.Exec("INSERT INTO literal_path VALUES(1)"); err != nil {
		t.Fatalf("filename was interpreted as URI parameters: %v", err)
	}
}

func TestOpenCorruptAndReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte(strings.Repeat("not sqlite data", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(context.Background(), corrupt); err == nil || db != nil {
		t.Fatal("corrupt database accepted")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystem permissions require native platform qualification")
	}
	readOnly := filepath.Join(dir, "readonly.db")
	if err := os.WriteFile(readOnly, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	probe, err := os.OpenFile(readOnly, os.O_RDWR, 0)
	if err == nil {
		probe.Close()
		t.Skip("current user bypasses filesystem write restrictions")
	}
	if db, err := Open(context.Background(), readOnly); err == nil || db != nil {
		t.Fatal("read-only database accepted")
	}
}

func TestOpenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	filename := filepath.Join(t.TempDir(), "cancelled.db")
	if db, err := Open(ctx, filename); !errors.Is(err, context.Canceled) || db != nil {
		t.Fatalf("got %v, %v", db, err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-cancelled open created a file")
	}
}

func TestConcurrentOpenExistingDatabase(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "shared.db")
	initial := openTestDB(t, filename)
	if _, err := initial.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < cap(errors); i++ {
		workers.Go(func() {
			db, err := Open(context.Background(), filename)
			if err != nil {
				errors <- err
				return
			}
			defer db.Close()
			_, err = db.Exec("INSERT INTO item DEFAULT VALUES")
			errors <- err
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := initial.QueryRow("SELECT count(*) FROM item").Scan(&count); err != nil || count != 4 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}

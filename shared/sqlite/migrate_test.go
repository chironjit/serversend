package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func migrationsForTest() []Migration {
	return []Migration{
		{1, "CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id))"},
		{2, "INSERT INTO parent(id) VALUES(1)"},
	}
}

func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestMigrateFreshUpgradeAndRepeat(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "migrations.db"))
	if err := Migrate(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	all := migrationsForTest()
	for _, history := range [][]Migration{all[:1], all, all} {
		if err := Migrate(context.Background(), db, history); err != nil {
			t.Fatal(err)
		}
		if version := schemaVersion(t, db); version != len(history) {
			t.Fatalf("version=%d want=%d", version, len(history))
		}
	}
	if _, err := db.Exec("INSERT INTO child VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO child VALUES(2,99)"); err == nil {
		t.Fatal("foreign key constraint not enforced")
	}
	if _, err := db.Exec("INSERT INTO child VALUES(3,NULL)"); err == nil {
		t.Fatal("not-null constraint not enforced")
	}
}

func TestMigrateRollback(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "rollback.db"))
	all := migrationsForTest()
	if err := Migrate(context.Background(), db, all[:1]); err != nil {
		t.Fatal(err)
	}
	failed := append(all, Migration{3, "CREATE TABLE transient(id INTEGER); INSERT INTO missing_table VALUES(1)"})
	if err := Migrate(context.Background(), db, failed); err == nil {
		t.Fatal("invalid migration accepted")
	}
	if version := schemaVersion(t, db); version != 1 {
		t.Fatalf("version after rollback=%d", version)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM parent").Scan(&count); err != nil || count != 0 {
		t.Fatalf("pending earlier migration not rolled back: count=%d error=%v", count, err)
	}
	if _, err := db.Exec("SELECT * FROM transient"); err == nil {
		t.Fatal("failed migration left a table")
	}
	if err := Migrate(context.Background(), db, all); err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}
}

func TestMigrateRejectsInvalidHistoryAndFutureSchema(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "invalid.db"))
	for _, history := range [][]Migration{
		{{0, "SELECT 1"}}, {{2, "SELECT 1"}}, {{1, " "}},
		{{1, "SELECT 1"}, {1, "SELECT 1"}}, {{1, "SELECT 1"}, {3, "SELECT 1"}},
	} {
		if err := Migrate(context.Background(), db, history); err == nil {
			t.Errorf("invalid history accepted: %#v", history)
		}
		if version := schemaVersion(t, db); version != 0 {
			t.Fatal("invalid history modified schema")
		}
	}
	if _, err := db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db, migrationsForTest()); !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("got %v, want ErrFutureSchema", err)
	}
	if schemaVersion(t, db) != 3 {
		t.Fatal("future version changed")
	}
	if _, err := db.Exec("PRAGMA user_version=-1"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db, migrationsForTest()); err == nil {
		t.Fatal("negative schema version accepted")
	}
	if err := Migrate(context.Background(), nil, nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestConcurrentMigrations(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "concurrent.db")
	a := openTestDB(t, filename)
	b := openTestDB(t, filename)
	var workers sync.WaitGroup
	errors := make(chan error, 4)
	for _, db := range []*sql.DB{a, b, a, b} {
		workers.Go(func() { errors <- Migrate(context.Background(), db, migrationsForTest()) })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := a.QueryRow("SELECT count(*) FROM parent").Scan(&count); err != nil || count != 1 || schemaVersion(t, a) != 2 {
		t.Fatalf("migration not applied exactly once: count=%d error=%v", count, err)
	}
}

func TestMigrateCancellation(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "cancel.db"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Migrate(ctx, db, migrationsForTest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled migration: %v", err)
	}
	if schemaVersion(t, db) != 0 {
		t.Fatal("cancelled migration changed schema")
	}
	// A held connection makes cancellation while waiting deterministic.
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = Migrate(ctx, db, migrationsForTest())
	conn.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting migration cancellation: %v", err)
	}
	if schemaVersion(t, db) != 0 {
		t.Fatal("waiting migration changed schema")
	}
	if err := Migrate(context.Background(), db, migrationsForTest()); err != nil {
		t.Fatalf("database unusable after cancellation: %v", err)
	}
}

func TestMigrateRunningStatementCancellation(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "running.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	migrations := []Migration{{1, `CREATE TABLE pending(id INTEGER);
		INSERT INTO pending WITH RECURSIVE numbers(n) AS (
			SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n<1000000000
		) SELECT sum(n) FROM numbers`}}
	if err := Migrate(ctx, db, migrations); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running statement cancellation: %v", err)
	}
	if schemaVersion(t, db) != 0 {
		t.Fatal("cancelled running migration changed schema version")
	}
	if _, err := db.Exec("SELECT * FROM pending"); err == nil {
		t.Fatal("cancelled running migration retained schema changes")
	}
	if err := Migrate(context.Background(), db, migrationsForTest()); err != nil {
		t.Fatalf("database unusable after interrupted statement: %v", err)
	}
}

func TestMigrateClosedDatabase(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "closed.db"))
	db.Close()
	if err := Migrate(context.Background(), db, migrationsForTest()); err == nil {
		t.Fatal("closed database accepted")
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestWriteCommitRollbackPanicAndCancellation(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "write.db"))
	if _, err := db.Exec("CREATE TABLE changes(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	injected := errors.New("injected failure")
	for _, scenario := range []string{"commit", "error", "panic", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var got error
			panicked := false
			func() {
				defer func() {
					if recover() != nil {
						panicked = true
					}
				}()
				got = Write(ctx, db, func(tx *sql.Tx) error {
					if _, err := tx.ExecContext(ctx, "INSERT INTO changes VALUES(1)"); err != nil {
						return err
					}
					switch scenario {
					case "error":
						return injected
					case "panic":
						panic(injected)
					case "cancel":
						cancel()
					}
					return nil
				})
			}()
			if scenario == "error" && !errors.Is(got, injected) {
				t.Fatalf("callback error lost: %v", got)
			}
			if scenario == "panic" && !panicked {
				t.Fatal("panic swallowed")
			}
			if scenario == "cancel" && !errors.Is(got, context.Canceled) {
				t.Fatalf("cancellation lost: %v", got)
			}
			if scenario == "commit" && got != nil {
				t.Fatal(got)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM changes").Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if scenario == "commit" {
				want = 1
			}
			if count != want {
				t.Fatalf("rows=%d want=%d", count, want)
			}
			if _, err := db.Exec("DELETE FROM changes"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := Write(ctx, nil, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("nil db accepted")
	}
	if err := Write(ctx, db, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	db.Close()
	if err := Write(ctx, db, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("closed db accepted")
	}
}

package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/sqlite"
)

func TestOpenSchemaAndFutureGuard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(migrations) {
		t.Fatalf("schema version=%d, error=%v", version, err)
	}
	for _, query := range []string{
		"INSERT INTO server_identity VALUES(2, ?, ?)",
		"INSERT INTO server_identity VALUES(1, 'short', ?)",
	} {
		var err error
		if strings.Contains(query, "'short'") {
			_, err = db.Exec(query, make([]byte, 32))
		} else {
			_, err = db.Exec(query, strings.Repeat("a", 32), make([]byte, 32))
		}
		if err == nil {
			t.Fatalf("constraint accepted: %s", query)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(ctx, dir); !errors.Is(err, sqlite.ErrFutureSchema) || db != nil {
		t.Fatalf("future schema: db=%v error=%v", db, err)
	}
}

func TestOpenRejectsUnsafePaths(t *testing.T) {
	for _, scenario := range []string{"missing", "relative", "public config", "public database", "symlink database", "corrupt database"} {
		t.Run(scenario, func(t *testing.T) {
			if runtime.GOOS == "windows" && (scenario == "public config" || scenario == "public database" || scenario == "symlink database") {
				t.Skip("POSIX permissions and symlink privileges require native platform qualification")
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(dir, Filename)
			switch scenario {
			case "missing":
				dir = filepath.Join(dir, "missing")
			case "relative":
				dir = "relative"
			case "public config":
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "public database":
				if err := os.WriteFile(filename, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink database":
				if err := os.Symlink(filepath.Join(t.TempDir(), "target"), filename); err != nil {
					t.Fatal(err)
				}
			case "corrupt database":
				if err := os.WriteFile(filename, []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if db, err := Open(context.Background(), dir); err == nil || db != nil {
				t.Fatalf("unsafe path accepted: db=%v error=%v", db, err)
			}
		})
	}
}

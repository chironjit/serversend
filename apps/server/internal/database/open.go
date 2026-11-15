// Package database owns the server's metadata schema. File contents belong on
// the data volume; credentials and metadata belong in this protected database.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chironjit/serversend/shared/sqlite"
)

const Filename = "serversend.sqlite"

var migrations = []sqlite.Migration{{Version: 1, SQL: `
CREATE TABLE server_identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    server_id TEXT NOT NULL UNIQUE CHECK (length(server_id) = 32),
    key_seed BLOB NOT NULL CHECK (typeof(key_seed) = 'blob' AND length(key_seed) = 32)
);
`}}

// Open requires an existing private configuration directory. It never follows
// a symlink at the database filename or changes existing file permissions.
func Open(ctx context.Context, configDir string) (*sql.DB, error) {
	if !filepath.IsAbs(configDir) {
		return nil, errors.New("database configuration directory must be absolute")
	}
	info, err := os.Stat(configDir)
	if err != nil || !info.IsDir() {
		return nil, errors.New("database configuration directory must exist")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("configuration directory must be private (mode 0700)")
	}
	filename := filepath.Join(configDir, Filename)
	info, err = os.Lstat(filename)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("metadata database must be a private regular file (mode 0600)")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect metadata database: %w", err)
	}
	db, err := sqlite.Open(ctx, filename)
	if err != nil {
		return nil, err
	}
	if err := sqlite.Migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

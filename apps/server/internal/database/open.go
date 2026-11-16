// Package database owns the server's metadata schema. File contents belong on
// the data volume; credentials and metadata belong in this protected database.
package database

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/chironjit/serversend/shared/sqlite"
)

const Filename = "serversend.sqlite"

// Open requires an existing private configuration directory. It never follows
// a symlink at the database filename or changes existing file permissions.
func Open(ctx context.Context, configDir string) (*sql.DB, error) {
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(configDir, Filename))
	if err != nil {
		return nil, err
	}
	if err := sqlite.Migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

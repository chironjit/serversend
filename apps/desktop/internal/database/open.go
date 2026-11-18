// Package database owns local profile and queue persistence. Credentials and
// vault keys belong in the operating system credential store, never these tables.
package database

import (
	"context"
	"database/sql"
	_ "embed"
	"path/filepath"

	"github.com/chironjit/serversend/shared/sqlite"
)

const Filename = "desktop.sqlite"

//go:embed schema.sql
var initialSchema string

//go:embed queue.sql
var queueSchema string

//go:embed vault.sql
var vaultSchema string

var migrations = []sqlite.Migration{{Version: 1, SQL: initialSchema}, {Version: 2, SQL: queueSchema}, {Version: 3, SQL: vaultSchema}}

// Open requires an existing private application directory. Windows callers must
// provision a user-only ACL. Startup does not create directories or repair access.
func Open(ctx context.Context, dir string) (*sql.DB, error) {
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(dir, Filename))
	if err != nil {
		return nil, err
	}
	if err := sqlite.Migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

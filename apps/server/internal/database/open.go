// Package database owns the server's metadata schema. File contents belong on
// the data volume; credentials and metadata belong in this protected database.
package database

import (
	"context"
	"database/sql"
	"fmt"
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
	if err := refuseLegacyFiles(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := sqlite.Migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// fileTreeVersion replaced the object store; earlier file metadata cannot be
// carried over because the change was made before release.
const fileTreeVersion = 11

var fileTables = []string{"trash_entries", "storage_audit", "chunks", "reservations", "deleted_objects", "versions", "files", "uploads", "directories", "storage_journal"}

func refuseLegacyFiles(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == 0 || version >= fileTreeVersion {
		return nil
	}
	for _, table := range []string{"uploads", "files", "directories"} {
		var exists, rows int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&rows); err != nil {
			return err
		}
		if rows != 0 {
			return ErrLegacyFiles
		}
	}
	return nil
}

// ResetFiles deletes all file metadata, keeping accounts, devices and
// identity, so a pre-release deployment can adopt the file tree. The caller
// holds the deployment locks and removes the data directory's contents.
func ResetFiles(ctx context.Context, configDir string) error {
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(configDir, Filename))
	if err != nil {
		return err
	}
	defer db.Close()
	return sqlite.Write(ctx, db, func(tx *sql.Tx) error {
		for _, table := range fileTables {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
				return err
			}
			if exists != 0 {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
					return err
				}
			}
		}
		var revisions int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('accounts') WHERE name='catalog_revision'`).Scan(&revisions); err != nil || revisions == 0 {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET catalog_revision=0`)
		return err
	})
}

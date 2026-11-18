package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"os"
	"path/filepath"

	"github.com/chironjit/serversend/shared/sqlite"
)

//go:embed migrations/*.sql
var schema embed.FS

// History is immutable; account/storage foundations extend the shipped v1.
var migrations = []sqlite.Migration{
	{Version: 1, SQL: migrationSQL("001_identity.sql")},
	{Version: 2, SQL: migrationSQL("002_accounts_uploads.sql")},
	{Version: 3, SQL: migrationSQL("003_authentication.sql")},
	{Version: 4, SQL: migrationSQL("004_two_factor.sql")},
	{Version: 5, SQL: migrationSQL("005_sessions_transfers.sql")},
	{Version: 6, SQL: migrationSQL("006_directories.sql")},
	{Version: 7, SQL: migrationSQL("007_pending_passwords.sql")},
	{Version: 8, SQL: migrationSQL("008_storage_recovery.sql")},
	{Version: 9, SQL: migrationSQL("009_device_refresh.sql")},
	{Version: 10, SQL: migrationSQL("010_installations.sql")},
	{Version: 11, SQL: migrationSQL("011_file_tree.sql")},
	{Version: 12, SQL: migrationSQL("012_retention.sql")},
	{Version: 13, SQL: migrationSQL("013_vault.sql")},
	{Version: 14, SQL: migrationSQL("014_account_administration.sql")},
}

func migrationSQL(name string) string {
	data, err := schema.ReadFile("migrations/" + name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// LatestVersion is the schema version this server migrates to.
func LatestVersion() int { return migrations[len(migrations)-1].Version }

// Installed reads an existing database's schema version and server ID without
// migrating it. A missing database reports 0. The caller holds the deployment
// locks, so neither can change before Open.
func Installed(ctx context.Context, configDir string) (version int, serverID string, err error) {
	filename := filepath.Join(configDir, Filename)
	if _, err := os.Lstat(filename); errors.Is(err, os.ErrNotExist) {
		return 0, "", nil
	}
	db, err := sqlite.OpenPrivate(ctx, filename)
	if err != nil {
		return 0, "", err
	}
	defer db.Close()
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version == 0 {
		return version, "", err
	}
	err = db.QueryRowContext(ctx, `SELECT server_id FROM server_identity WHERE singleton=1`).Scan(&serverID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return version, serverID, err
}

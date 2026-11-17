package database

import (
	"embed"

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
}

func migrationSQL(name string) string {
	data, err := schema.ReadFile("migrations/" + name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

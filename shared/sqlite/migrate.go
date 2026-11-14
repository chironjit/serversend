package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrFutureSchema = errors.New("SQLite schema is newer than supported")

// Migration is trusted, application-owned SQL. Versions must be consecutive,
// starting at 1, and the complete history must be supplied on every call.
// SQL must not manage transactions, modify user_version, or issue connection
// configuration statements; Migrate owns the transaction and version marker.
type Migration struct {
	Version int
	SQL     string
}

// Migrate atomically applies all pending migrations to a database opened by
// Open. BEGIN IMMEDIATE serializes concurrent migrators before reading the
// schema marker. A failure rolls back both pending schema changes and version.
func Migrate(ctx context.Context, db *sql.DB, migrations []Migration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if db == nil {
		return errors.New("SQLite database is required")
	}
	for i, migration := range migrations {
		if migration.Version != i+1 || strings.TrimSpace(migration.SQL) == "" {
			return fmt.Errorf("invalid migration at index %d: require version %d and nonempty SQL", i, i+1)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration: %w", err)
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read SQLite schema version: %w", err)
	}
	if current < 0 {
		return errors.New("invalid negative SQLite schema version")
	}
	if current > len(migrations) {
		return fmt.Errorf("%w: database=%d supported=%d", ErrFutureSchema, current, len(migrations))
	}
	for _, migration := range migrations[current:] {
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply SQLite migration %d: %w", migration.Version, err)
		}
		// PRAGMA does not accept placeholders. Version is a validated integer.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.Version)); err != nil {
			return fmt.Errorf("record SQLite migration %d: %w", migration.Version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migrations: %w", err)
	}
	return nil
}

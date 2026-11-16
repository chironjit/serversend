package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Write executes a short, serialized transaction. The callback must use tx, not
// db (the pool has one connection), and must not perform network/filesystem work.
// Errors and panics roll back. There is no implicit retry of side effects.
func Write(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	if db == nil || fn == nil {
		return errors.New("database and transaction callback are required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite write: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite write: %w", err)
	}
	return nil
}

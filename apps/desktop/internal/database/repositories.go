package database

import (
	"context"
	"database/sql"

	"github.com/chironjit/serversend/shared/sqlite"
)

// Repository groups local profile and queue operations on one database handle.
// Writes use callback-scoped transactions; profile reads use a consistent
// transaction snapshot so a trust pin and its approved routes agree.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) write(ctx context.Context, operation func(*sql.Tx) error) error {
	return sqlite.Write(ctx, r.db, operation)
}

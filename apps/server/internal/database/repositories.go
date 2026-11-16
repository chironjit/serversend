package database

import (
	"context"
	"database/sql"

	"github.com/chironjit/serversend/shared/sqlite"
)

// Repository groups server metadata operations on one database handle. Methods
// that modify multiple rows use a short transaction and never let it escape
// its callback. Authorization and business rules belong to the caller.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) write(ctx context.Context, operation func(*sql.Tx) error) error {
	return sqlite.Write(ctx, r.db, operation)
}

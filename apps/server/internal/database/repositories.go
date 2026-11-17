package database

import (
	"context"
	"database/sql"
	"sync"

	"github.com/chironjit/serversend/shared/sqlite"
)

// Repository groups server metadata operations on one database handle. Methods
// that modify multiple rows use a short transaction and never let it escape
// its callback. Authorization and business rules belong to the caller.
type Repository struct {
	db *sql.DB

	journalMu sync.Mutex
	applied   int64 // journal entries applied to the filesystem
	cleared   int64 // and deleted from the journal
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// write also forgets applied journal entries within the same transaction,
// saving a commit per filesystem change. Until then an applied sequence
// stays in the journal; replaying the latest sequence again is harmless.
func (r *Repository) write(ctx context.Context, operation func(*sql.Tx) error) error {
	r.journalMu.Lock()
	applied, cleared := r.applied, r.cleared
	r.journalMu.Unlock()
	err := sqlite.Write(ctx, r.db, func(tx *sql.Tx) error {
		if applied > cleared {
			if _, err := tx.ExecContext(ctx, `DELETE FROM storage_journal WHERE id<=?`, applied); err != nil {
				return err
			}
		}
		return operation(tx)
	})
	if err == nil && applied > cleared {
		r.journalMu.Lock()
		r.cleared = max(r.cleared, applied)
		r.journalMu.Unlock()
	}
	return err
}

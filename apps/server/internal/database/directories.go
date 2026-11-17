package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (r *Repository) CheckDirectory(ctx context.Context, account, parent protocol.ID) error {
	if parent == "" {
		return nil
	}
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM directories WHERE account_id=? AND id=?`, account, parent).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Error{Code: api.NotFound, Message: "Destination folder does not exist for this account"}
	}
	return err
}

// CreateDirectory creates a folder, or returns the existing folder of that
// name, so uploading a folder again merges into it. The folder appears in
// the data directory once the journal is applied.
func (r *Repository) CreateDirectory(ctx context.Context, account protocol.ID, request api.CreateDirectoryRequest) (api.CatalogEntry, error) {
	entry := api.CatalogEntry{ID: request.ID, ParentID: request.ParentID, Mode: protocol.Standard, Kind: api.Directory, Standard: request.Standard}
	err := r.write(ctx, func(tx *sql.Tx) error {
		if request.ParentID != "" {
			var id string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM directories WHERE account_id=? AND id=?`, account, request.ParentID).Scan(&id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return api.Error{Code: api.NotFound, Message: "Destination folder does not exist for this account"}
				}
				return err
			}
		}
		var existing protocol.ID
		var revision protocol.Revision
		err := tx.QueryRowContext(ctx, `SELECT id,CAST(revision AS TEXT) FROM directories WHERE account_id=? AND parent_id=? AND name=?`, account, request.ParentID, request.Standard.Name).Scan(&existing, &revision)
		if err == nil {
			entry.ID, entry.HeadRevision = existing, revision
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var occupied int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM files WHERE account_id=?1 AND (id=?2 OR (parent_id=?3 AND name=?4 AND hidden=0))) + (SELECT count(*) FROM uploads WHERE account_id=?1 AND file_id=?2) + (SELECT count(*) FROM directories WHERE account_id=?1 AND id=?2)`,
			account, request.ID, request.ParentID, request.Standard.Name).Scan(&occupied); err != nil {
			return err
		}
		if occupied != 0 {
			return api.Error{Code: api.Conflict, Message: "A file with this name, or an entry with this ID, already exists"}
		}
		if err := tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING CAST(catalog_revision AS TEXT)`, account).Scan(&entry.HeadRevision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO directories(account_id,id,parent_id,name,revision) VALUES(?,?,?,?,?)`, account, request.ID, request.ParentID, request.Standard.Name, entry.HeadRevision); err != nil {
			return err
		}
		path, err := dirPath(ctx, tx, account, request.ID)
		if err != nil {
			return err
		}
		return journal(ctx, tx, storage.Op{Kind: "mkdir", Target: path})
	})
	return entry, err
}

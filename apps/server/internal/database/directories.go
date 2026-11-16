package database

import (
	"context"
	"database/sql"
	"errors"
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
		var occupied int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM files WHERE account_id=? AND id=?) + (SELECT count(*) FROM uploads WHERE account_id=? AND file_id=?)`, account, request.ID, account, request.ID).Scan(&occupied); err != nil {
			return err
		}
		if occupied != 0 {
			return api.Error{Code: api.Conflict, Message: "This ID is already used by a file"}
		}
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM directories WHERE account_id=? AND (id=? OR (parent_id=? AND name=?))`, account, request.ID, request.ParentID, request.Standard.Name).Scan(&existing)
		if err == nil {
			return api.Error{Code: api.Conflict, Message: "A folder with this name or ID already exists"}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING CAST(catalog_revision AS TEXT)`, account).Scan(&entry.HeadRevision); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO directories(account_id,id,parent_id,name,revision) VALUES(?,?,?,?,?)`, account, request.ID, request.ParentID, request.Standard.Name, entry.HeadRevision)
		return err
	})
	return entry, err
}

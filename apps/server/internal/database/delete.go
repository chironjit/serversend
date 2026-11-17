package database

import (
	"context"
	"database/sql"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"strings"
	"time"
)

// DeleteEntry removes a complete tree atomically. Active uploads must finish first.
func (r *Repository) DeleteEntry(ctx context.Context, account, id protocol.ID) ([]protocol.ID, error) {
	objects := []protocol.ID{}
	err := r.write(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM files WHERE account_id=? AND id=?)+(SELECT count(*) FROM directories WHERE account_id=? AND id=?)`, account, id, account, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return api.Error{Code: api.NotFound, Message: "File or folder no longer exists"}
		}
		// Materialize IDs before deleting any rows, always scoped to the owner.
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE deletion_ids(id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		defer tx.ExecContext(context.Background(), `DROP TABLE IF EXISTS deletion_ids`)
		if _, err := tx.ExecContext(ctx, `INSERT INTO deletion_ids WITH RECURSIVE tree(id) AS (SELECT ? UNION SELECT d.id FROM directories d JOIN tree t ON d.parent_id=t.id WHERE d.account_id=?) SELECT id FROM tree UNION SELECT f.id FROM files f JOIN tree t ON f.parent_id=t.id WHERE f.account_id=?`, id, account, account); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM uploads WHERE account_id=? AND (file_id IN deletion_ids OR parent_id IN deletion_ids) AND (state='finalizing' OR (state='open' AND (expires_at IS NULL OR expires_at>?)))`, account, time.Now().UnixMilli()).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return api.Error{Code: api.Conflict, Message: "Wait for uploads in this file or folder to finish before deleting"}
		}
		rows, err := tx.QueryContext(ctx, `SELECT object_id FROM chunks WHERE account_id=? AND upload_id IN (SELECT id FROM uploads WHERE account_id=? AND (file_id IN deletion_ids OR parent_id IN deletion_ids))`, account, account)
		if err != nil {
			return err
		}
		for rows.Next() {
			var object protocol.ID
			if err := rows.Scan(&object); err != nil {
				rows.Close()
				return err
			}
			objects = append(objects, object)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, object := range objects {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_objects(id) VALUES(?)`, object); err != nil {
				return err
			}
		}
		for _, query := range []string{
			`DELETE FROM versions WHERE account_id=? AND file_id IN deletion_ids`,
			`DELETE FROM chunks WHERE account_id=? AND upload_id IN (SELECT id FROM uploads WHERE account_id=? AND (file_id IN deletion_ids OR parent_id IN deletion_ids))`,
			`DELETE FROM uploads WHERE account_id=? AND (file_id IN deletion_ids OR parent_id IN deletion_ids)`,
			`DELETE FROM files WHERE account_id=? AND id IN deletion_ids`,
			`DELETE FROM directories WHERE account_id=? AND id IN deletion_ids`,
		} {
			args := []any{account}
			if strings.HasPrefix(query, "DELETE FROM chunks") {
				args = append(args, account)
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=?`, account)
		return err
	})
	return objects, err
}
func (r *Repository) DeletedObjects(ctx context.Context) ([]protocol.ID, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM deleted_objects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []protocol.ID{}
	for rows.Next() {
		var id protocol.ID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *Repository) ForgetDeletedObject(ctx context.Context, id protocol.ID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM deleted_objects WHERE id=?`, id)
	return err
}

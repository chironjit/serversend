package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// DeleteEntry removes a complete tree atomically, cancelling unfinished
// uploads into it: interrupted clients otherwise leave them blocking deletion
// until expiry. Their reservations cascade. The visible tree and every
// file's retained versions are journaled for removal through the trash, so
// removal is a single rename for anyone browsing the data directory.
func (r *Repository) DeleteEntry(ctx context.Context, account, id protocol.ID, purgeAt ...time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if len(purgeAt) > 0 {
			var found int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM trash_entries WHERE account_id=? AND id=?`, account, id).Scan(&found); err != nil {
				return err
			}
			if found == 0 {
				return api.Error{Code: api.Conflict, Message: "Only items in trash can be purged"}
			}
			if err := lifecycleAudit(ctx, tx, account, "trash_purge", purgeAt[0]); err != nil {
				return err
			}
		}
		var parent protocol.ID
		var name string
		var directory bool
		err := tx.QueryRowContext(ctx, `SELECT parent_id,name,0 FROM files WHERE account_id=?1 AND id=?2 AND hidden=0 UNION ALL SELECT parent_id,name,1 FROM directories WHERE account_id=?1 AND id=?2`, account, id).Scan(&parent, &name, &directory)
		if errors.Is(err, sql.ErrNoRows) {
			return api.Error{Code: api.NotFound, Message: "File or folder no longer exists"}
		}
		if err != nil {
			return err
		}
		visible, err := filePath(ctx, tx, account, parent, name)
		if directory {
			visible, err = dirPath(ctx, tx, account, id)
		}
		if err != nil {
			return err
		}
		// Materialize IDs before deleting any rows, always scoped to the owner.
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE deletion_ids(id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		defer tx.ExecContext(context.Background(), `DROP TABLE IF EXISTS deletion_ids`)
		if _, err := tx.ExecContext(ctx, `INSERT INTO deletion_ids WITH RECURSIVE tree(id) AS (SELECT ? UNION SELECT d.id FROM directories d JOIN tree t ON d.parent_id=t.id WHERE d.account_id=?) SELECT id FROM tree UNION SELECT f.id FROM files f JOIN tree t ON f.parent_id=t.id WHERE f.account_id=?`, id, account, account); err != nil {
			return err
		}
		trash := layout.TrashEntry(newJournalID())
		ops := []storage.Op{{Kind: "move", Source: visible, Target: trash}, {Kind: "remove", Target: trash}}
		rows, err := tx.QueryContext(ctx, `SELECT 'f',id FROM files WHERE account_id=? AND id IN deletion_ids UNION ALL SELECT 'u',id FROM uploads WHERE account_id=? AND state='open' AND (file_id IN deletion_ids OR parent_id IN deletion_ids)`, account, account)
		if err != nil {
			return err
		}
		for rows.Next() {
			var kind string
			var entry protocol.ID
			if err := rows.Scan(&kind, &entry); err != nil {
				rows.Close()
				return err
			}
			if kind == "u" {
				ops = append(ops, storage.Op{Kind: "remove", Target: layout.Upload(entry)})
				continue
			}
			versions := layout.TrashEntry(newJournalID())
			ops = append(ops, storage.Op{Kind: "move", Source: layout.VersionsOf(account, entry), Target: versions}, storage.Op{Kind: "remove", Target: versions})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if err := journal(ctx, tx, ops...); err != nil {
			return err
		}
		for _, query := range []string{
			`DELETE FROM trash_entries WHERE account_id=? AND id IN deletion_ids`,
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
}

func newJournalID() protocol.ID {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return protocol.ID(hex.EncodeToString(value))
}

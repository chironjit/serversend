package database

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/apps/server/internal/storage"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// Capacity limits retained version bytes plus complete open-upload reservations.
// Available is filesystem headroom after the operator's free-space floor.
type Capacity struct{ Global, Account, Available int64 }

func checkCapacity(ctx context.Context, tx *sql.Tx, t Transfer, c Capacity) error {
	var total, owned, pending int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT coalesce(sum(total_bytes),0) FROM versions)+(SELECT coalesce(sum(bytes),0) FROM reservations),
 (SELECT coalesce(sum(total_bytes),0) FROM versions WHERE account_id=?)+(SELECT coalesce(sum(bytes),0) FROM reservations WHERE account_id=?),
 (SELECT coalesce(sum(bytes),0) FROM reservations)-(SELECT coalesce(sum(byte_count),0) FROM chunks c JOIN reservations r ON r.account_id=c.account_id AND r.upload_id=c.upload_id)`, t.AccountID, t.AccountID).Scan(&total, &owned, &pending)
	if err != nil {
		return err
	}
	if (c.Global > 0 && (total > c.Global || t.TransmittedBytes > c.Global-total)) ||
		(c.Account > 0 && (owned > c.Account || t.TransmittedBytes > c.Account-owned)) ||
		pending > c.Available || t.TransmittedBytes > c.Available-pending {
		return api.Error{Code: api.CapacityExhausted, Message: "Insufficient storage capacity for this upload"}
	}
	return nil
}

// RetireUploads cancels one upload (if id is set) and expires overdue ones,
// journaling removal of their files and releasing their reservations.
// Terminal upload rows remain as idempotency tombstones.
func (r *Repository) RetireUploads(ctx context.Context, now time.Time, account, id protocol.ID) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if id != "" {
			t, err := scanTransfer(tx.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM uploads WHERE account_id=? AND id=? AND file_id IS NOT NULL`, account, id))
			if err != nil {
				return err
			}
			if t.State == protocol.UploadCommitted || t.State == protocol.UploadConflicted {
				return api.Error{Code: api.Conflict, Message: "A finalized upload cannot be cancelled"}
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM uploads WHERE state='open' AND (expires_at<=? OR (account_id=? AND id=?))`, now.UnixMilli(), account, id)
		if err != nil {
			return err
		}
		var retired []protocol.ID
		for rows.Next() {
			var upload protocol.ID
			if err := rows.Scan(&upload); err != nil {
				rows.Close()
				return err
			}
			retired = append(retired, upload)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, upload := range retired {
			if err := journal(ctx, tx, storage.Op{Kind: "remove", Source: "", Target: layout.Upload(upload)}); err != nil {
				return err
			}
		}
		for _, query := range []string{
			`UPDATE uploads SET state='cancelled' WHERE account_id=? AND id=? AND state='open'`,
			`UPDATE uploads SET state='expired' WHERE state='open' AND expires_at<=?`,
		} {
			args := []any{account, id}
			if strings.Contains(query, "expired") {
				args = []any{now.UnixMilli()}
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
		}
		for _, query := range []string{
			`DELETE FROM chunks WHERE EXISTS (SELECT 1 FROM uploads u WHERE u.account_id=chunks.account_id AND u.id=chunks.upload_id AND u.state IN ('expired','cancelled'))`,
			`DELETE FROM reservations WHERE EXISTS (SELECT 1 FROM uploads u WHERE u.account_id=reservations.account_id AND u.id=reservations.upload_id AND u.state IN ('expired','cancelled'))`,
		} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) UnwrittenBytes(ctx context.Context, account, upload protocol.ID) (int64, error) {
	var remaining int64
	err := r.db.QueryRowContext(ctx, `SELECT expected_bytes-coalesce((SELECT sum(byte_count) FROM chunks WHERE account_id=? AND upload_id=?),0) FROM uploads WHERE account_id=? AND id=?`, account, upload, account, upload).Scan(&remaining)
	return remaining, err
}

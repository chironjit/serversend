package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

type DeviceInfo = api.DeviceInfo

func (r *Repository) Devices(ctx context.Context, account protocol.ID, now time.Time, before ...protocol.ID) ([]DeviceInfo, error) {
	query := `SELECT d.id,d.name,d.platform,d.app_version,d.created_at,d.last_seen,d.last_refresh,d.last_factor,d.revoked_at,d.expires_at,d.refresh_hash IS NOT NULL, a.auth_epoch=d.auth_epoch AND a.disabled=0 AND a.state='active' FROM device_sessions d JOIN accounts a ON a.id=d.account_id WHERE d.account_id=?`
	args := []any{account}
	if len(before) > 0 && before[0] != "" {
		query += ` AND (d.created_at,d.id)<(SELECT created_at,id FROM device_sessions WHERE account_id=? AND id=?)`
		args = append(args, account, before[0])
	}
	query += ` ORDER BY d.created_at DESC,d.id DESC`
	if len(before) > 0 {
		query += ` LIMIT 100`
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceInfo{}
	for rows.Next() {
		var d DeviceInfo
		var created, seen, refresh, factor, expiry int64
		var revoked sql.NullInt64
		var rotating, live bool
		if err = rows.Scan(&d.ID, &d.Name, &d.Platform, &d.AppVersion, &created, &seen, &refresh, &factor, &revoked, &expiry, &rotating, &live); err != nil {
			return nil, err
		}
		d.SignedInAt = time.UnixMilli(created).UTC()
		d.LastSeenAt = time.UnixMilli(seen).UTC()
		d.Status = "authorized"
		if rotating {
			t := time.UnixMilli(refresh).UTC()
			d.LastRefreshAt = &t
			idle := t.Add(RefreshIdleLifetime)
			d.IdleExpiresAt = &idle
			due := time.UnixMilli(factor).UTC().Add(FactorLifetime)
			d.ReauthenticateAt = &due
			if !idle.After(now) {
				d.Status = "expired"
			} else if !due.After(now) {
				d.Status = "totp_required"
			}
		} else if expiry <= now.UnixMilli() {
			d.Status = "expired"
		}
		if revoked.Valid {
			t := time.UnixMilli(revoked.Int64).UTC()
			d.RevokedAt = &t
			d.Status = "revoked"
		} else if !live {
			d.Status = "revoked"
		}
		d.RecentlyActive = d.Status == "authorized" && seen > now.Add(-90*time.Second).UnixMilli()
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *Repository) RevokeDevice(ctx context.Context, actor protocol.ID, epoch int64, account, device protocol.ID, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := checkAdmin(ctx, tx, actor, epoch); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=coalesce(revoked_at,?) WHERE id=? AND account_id=?`, now.UnixMilli(), device, account)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id) VALUES(?,?,'revoke_device',?,?)`, actor, account, now.UnixMilli(), device)
		return err
	})
}

// RevokeOwnDevices rechecks the caller inside the same write transaction.
func (r *Repository) RevokeOwnDevices(ctx context.Context, source DeviceSession, tokenHash string, target protocol.ID, others bool, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		var live int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM device_sessions d JOIN accounts a ON a.id=d.account_id WHERE d.id=? AND d.account_id=? AND d.revoked_at IS NULL AND a.auth_epoch=d.auth_epoch AND d.auth_epoch=? AND a.disabled=0 AND a.state='active'
 AND ((d.token_hash=? AND d.expires_at>?) OR d.id IN (SELECT device_id FROM device_access WHERE token_hash=? AND expires_at>?))
 AND (d.refresh_hash IS NULL OR (d.last_refresh>? AND d.last_factor>?))`, source.ID, source.AccountID, source.Epoch, tokenHash, now.UnixMilli(), tokenHash, now.UnixMilli(), now.Add(-RefreshIdleLifetime).UnixMilli(), now.Add(-FactorLifetime).UnixMilli()).Scan(&live); err != nil {
			return err
		}
		if live != 1 {
			return ErrAuthConflict
		}
		predicate := "id=?"
		id := target
		if others {
			predicate = "id<>?"
			id = source.ID
		}

		if !others {
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM device_sessions WHERE account_id=? AND id=?`, source.AccountID, target).Scan(&live); err != nil {
				return err
			}
		}
		// Audit and revoke in SQL so signing out many devices does not build
		// an unbounded list in application memory.
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id)
          SELECT ?,account_id,'revoke_device',?,id FROM device_sessions WHERE account_id=? AND `+predicate+` AND revoked_at IS NULL`, source.AccountID, now.UnixMilli(), source.AccountID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=? WHERE account_id=? AND `+predicate+` AND revoked_at IS NULL`, now.UnixMilli(), source.AccountID, id); err != nil {
			return err
		}

		return nil
	})
}

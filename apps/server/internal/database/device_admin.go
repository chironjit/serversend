package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

type DeviceInfo struct {
	ID               protocol.ID `json:"id"`
	Name             string      `json:"name"`
	Platform         string      `json:"platform"`
	AppVersion       string      `json:"app_version"`
	SignedInAt       time.Time   `json:"signed_in_at"`
	LastSeenAt       time.Time   `json:"last_seen_at"`
	LastRefreshAt    *time.Time  `json:"last_refresh_at,omitempty"`
	IdleExpiresAt    *time.Time  `json:"idle_expires_at,omitempty"`
	ReauthenticateAt *time.Time  `json:"reauthenticate_at,omitempty"`
	RevokedAt        *time.Time  `json:"revoked_at,omitempty"`
	Status           string      `json:"status"`
	RecentlyActive   bool        `json:"recently_active"`
}

func (r *Repository) Devices(ctx context.Context, account protocol.ID, now time.Time) ([]DeviceInfo, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT d.id,d.name,d.platform,d.app_version,d.created_at,d.last_seen,d.last_refresh,d.last_factor,d.revoked_at,d.expires_at,d.refresh_hash IS NOT NULL, a.auth_epoch=d.auth_epoch AND a.disabled=0 AND a.state='active' FROM device_sessions d JOIN accounts a ON a.id=d.account_id WHERE d.account_id=? ORDER BY d.created_at DESC,d.id`, account)
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

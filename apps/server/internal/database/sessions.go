package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

// DeviceSession is one signed-in device. The access token itself is never
// stored; authorization is rechecked against live account state on every use.
type DeviceSession struct {
	ID                   protocol.ID
	AccountID            protocol.ID
	Username             string
	Role                 string
	Name                 string
	Epoch                int64
	ExpiresAt            time.Time
	CreatedAt            time.Time
	RefreshHash          string
	Platform, AppVersion string
}

// CreateSession succeeds only while the account is still exactly as it was
// when the second factor was verified.
func (r *Repository) CreateSession(ctx context.Context, s DeviceSession, tokenHash string, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at)
			SELECT ?,id,?,?,auth_epoch,?,? FROM accounts WHERE id=? AND auth_epoch=? AND state='active' AND totp_enrolled=1 AND disabled=0`,
			s.ID, s.Name, tokenHash, now.UnixMilli(), s.ExpiresAt.UnixMilli(), s.AccountID, s.Epoch)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE device_sessions SET refresh_hash=?,last_refresh=?,last_factor=?,last_seen=?,platform=?,app_version=? WHERE id=?`, nullableHash(s.RefreshHash), now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), s.Platform, s.AppVersion, s.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET recovery_kind='' WHERE id=?`, s.AccountID)
		return err
	})
}

// SessionByToken returns sql.ErrNoRows for unknown, expired and revoked
// sessions alike: disablement, resets and TOTP resets change auth_epoch.
func (r *Repository) SessionByToken(ctx context.Context, tokenHash string, now time.Time) (DeviceSession, error) {
	var s DeviceSession
	var expires, created int64
	err := r.db.QueryRowContext(ctx, `SELECT d.id,d.account_id,a.username,a.role,d.name,d.auth_epoch,d.expires_at,d.created_at FROM device_sessions d JOIN accounts a ON a.id=d.account_id
		WHERE d.id IN (SELECT id FROM device_sessions WHERE token_hash=? AND expires_at>? UNION ALL SELECT device_id FROM device_access WHERE token_hash=? AND expires_at>?)
        AND d.revoked_at IS NULL AND (d.refresh_hash IS NULL OR (d.last_refresh>? AND d.last_factor>?)) AND a.auth_epoch=d.auth_epoch AND a.state='active' AND a.totp_enrolled=1 AND a.disabled=0`,
		tokenHash, now.UnixMilli(), tokenHash, now.UnixMilli(), now.Add(-RefreshIdleLifetime).UnixMilli(), now.Add(-FactorLifetime).UnixMilli()).Scan(&s.ID, &s.AccountID, &s.Username, &s.Role, &s.Name, &s.Epoch, &expires, &created)
	s.ExpiresAt = time.UnixMilli(expires).UTC()
	s.CreatedAt = time.UnixMilli(created).UTC()
	if err == nil {
		_, err = r.db.ExecContext(ctx, `UPDATE device_sessions SET last_seen=? WHERE id=? AND last_seen<?`, now.UnixMilli(), s.ID, now.Add(-30*time.Second).UnixMilli())
	}
	return s, err
}

func (r *Repository) DeleteSession(ctx context.Context, tokenHash string, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=? WHERE (token_hash=? AND expires_at>?) OR id IN (SELECT device_id FROM device_access WHERE token_hash=? AND expires_at>?)`, now.UnixMilli(), tokenHash, now.UnixMilli(), tokenHash, now.UnixMilli())
		return err
	})
}

func nullableHash(value string) any {
	if value == "" {
		return nil
	}
	return value
}

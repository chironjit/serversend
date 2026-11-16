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
	ID        protocol.ID
	AccountID protocol.ID
	Username  string
	Role      string
	Name      string
	Epoch     int64
	ExpiresAt time.Time
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
		_, err = tx.ExecContext(ctx, `DELETE FROM device_sessions WHERE expires_at<=?`, now.UnixMilli())
		return err
	})
}

// SessionByToken returns sql.ErrNoRows for unknown, expired and revoked
// sessions alike: disablement, resets and TOTP resets change auth_epoch.
func (r *Repository) SessionByToken(ctx context.Context, tokenHash string, now time.Time) (DeviceSession, error) {
	var s DeviceSession
	var expires int64
	err := r.db.QueryRowContext(ctx, `SELECT d.id,d.account_id,a.username,a.role,d.name,d.auth_epoch,d.expires_at FROM device_sessions d JOIN accounts a ON a.id=d.account_id
		WHERE d.token_hash=? AND d.expires_at>? AND a.auth_epoch=d.auth_epoch AND a.state='active' AND a.totp_enrolled=1 AND a.disabled=0`,
		tokenHash, now.UnixMilli()).Scan(&s.ID, &s.AccountID, &s.Username, &s.Role, &s.Name, &s.Epoch, &expires)
	s.ExpiresAt = time.UnixMilli(expires).UTC()
	return s, err
}

func (r *Repository) DeleteSession(ctx context.Context, tokenHash string) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM device_sessions WHERE token_hash=?`, tokenHash)
		return err
	})
}

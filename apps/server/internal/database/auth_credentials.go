package database

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAuthConflict = errors.New("authentication state changed")

// Credentials is private authentication input, never an API response.
type Credentials struct {
	Account
	PasswordHash       string        `json:"-"`
	TemporaryExpiresAt sql.NullInt64 `json:"-"`
	Revision, Epoch    int64
	FailedAttempts     int
	BlockedUntil       int64
}

const credentialColumns = `id,username,role,state,totp_enrolled,disabled,created_at,password_hash,temporary_expires_at,credential_revision,auth_epoch,failed_attempts,blocked_until`

func scanCredentials(row interface{ Scan(...any) error }) (Credentials, error) {
	var c Credentials
	var created int64
	err := row.Scan(&c.ID, &c.Username, &c.Role, &c.Status.State, &c.Status.TOTPEnrolled, &c.Status.Disabled, &created, &c.PasswordHash, &c.TemporaryExpiresAt, &c.Revision, &c.Epoch, &c.FailedAttempts, &c.BlockedUntil)
	c.CreatedAt = time.UnixMilli(created).UTC()
	return c, err
}

func (r *Repository) CredentialsByUsername(ctx context.Context, username string) (Credentials, error) {
	return scanCredentials(r.db.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM accounts WHERE username=?`, username))
}

// RecordPasswordFailure serializes persistent account backoff. The applied
// delay is bounded at five minutes. Revision/epoch checks exclude stale work
// after password replacement, reset, disablement or re-enablement.
func (r *Repository) RecordPasswordFailure(ctx context.Context, c Credentials, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		var failures int
		if err := tx.QueryRowContext(ctx, `SELECT failed_attempts FROM accounts WHERE id=? AND credential_revision=? AND auth_epoch=?`, c.ID, c.Revision, c.Epoch).Scan(&failures); err != nil {
			return err
		}
		failures = min(failures+1, 10)
		delay := min(time.Second*time.Duration(1<<uint(failures-1)), 5*time.Minute)
		_, err := tx.ExecContext(ctx, `UPDATE accounts SET failed_attempts=?,blocked_until=? WHERE id=? AND credential_revision=? AND auth_epoch=?`, failures, now.Add(delay).UnixMilli(), c.ID, c.Revision, c.Epoch)
		return err
	})
}

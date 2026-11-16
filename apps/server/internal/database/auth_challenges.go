package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

type AuthChallenge struct {
	TokenHash       string
	AccountID       protocol.ID
	Scope           string
	Revision, Epoch int64
	ExpiresAt       time.Time
}

// BeginChallenge verifies the state snapshot after expensive password work.
// Only one outstanding password attempt per account survives a new login.
func (r *Repository) BeginChallenge(ctx context.Context, c Credentials, ch AuthChallenge, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		current, err := scanCredentials(tx.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM accounts WHERE id=?`, c.ID))
		if err != nil {
			return err
		}
		if current.Revision != c.Revision || current.Epoch != c.Epoch || current.Status != c.Status || current.Status.Disabled || current.BlockedUntil > now.UnixMilli() {
			return ErrAuthConflict
		}
		if current.Status.State == protocol.PasswordRequired && (!current.TemporaryExpiresAt.Valid || current.TemporaryExpiresAt.Int64 <= now.UnixMilli()) {
			return ErrAuthConflict
		}
		expectedScope := "second_factor"
		if current.Status.State == protocol.PasswordRequired {
			expectedScope = "replace_password"
		} else if current.Status.State == protocol.TOTPRequired {
			expectedScope = "enroll_totp"
		}
		if ch.Scope != expectedScope {
			return ErrAuthConflict
		}
		if ch.AccountID != c.ID || ch.Epoch != c.Epoch || ch.Revision != c.Revision || !ch.ExpiresAt.After(now) || ch.ExpiresAt.After(now.Add(10*time.Minute)) {
			return ErrAuthConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE account_id=? OR expires_at<=?`, c.ID, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_challenges(token_hash,account_id,scope,credential_revision,auth_epoch,expires_at) VALUES(?,?,?,?,?,?)`, ch.TokenHash, ch.AccountID, ch.Scope, ch.Revision, ch.Epoch, ch.ExpiresAt.UnixMilli()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET failed_attempts=0,blocked_until=0 WHERE id=?`, c.ID)
		return err
	})
}

func (r *Repository) Challenge(ctx context.Context, tokenHash string, now time.Time) (Credentials, AuthChallenge, error) {
	var ch AuthChallenge
	var expires int64
	err := r.db.QueryRowContext(ctx, `SELECT token_hash,account_id,scope,credential_revision,auth_epoch,expires_at FROM auth_challenges WHERE token_hash=? AND expires_at>?`, tokenHash, now.UnixMilli()).Scan(&ch.TokenHash, &ch.AccountID, &ch.Scope, &ch.Revision, &ch.Epoch, &expires)
	if err != nil {
		return Credentials{}, ch, err
	}
	c, err := scanCredentials(r.db.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM accounts WHERE id=?`, ch.AccountID))
	ch.ExpiresAt = time.UnixMilli(expires).UTC()
	if err != nil {
		return c, ch, err
	}
	if c.Revision != ch.Revision || c.Epoch != ch.Epoch || c.Status.Disabled {
		return c, ch, ErrAuthConflict
	}
	return c, ch, nil
}

func (r *Repository) ReplacePassword(ctx context.Context, ch AuthChallenge, passwordHash string, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET password_hash=?,temporary_expires_at=NULL,credential_revision=credential_revision+1,auth_epoch=auth_epoch+1,failed_attempts=0,blocked_until=0,state=CASE WHEN totp_enrolled=1 THEN 'active' ELSE 'totp_required' END WHERE id=? AND state='password_required' AND disabled=0 AND credential_revision=? AND auth_epoch=? AND temporary_expires_at>? AND EXISTS(SELECT 1 FROM auth_challenges WHERE token_hash=? AND account_id=accounts.id AND scope='replace_password' AND expires_at>?)`, passwordHash, ch.AccountID, ch.Revision, ch.Epoch, now.UnixMilli(), ch.TokenHash, now.UnixMilli())
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE account_id=?`, ch.AccountID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,'replace_password',?)`, ch.AccountID, ch.AccountID, now.UnixMilli())
		return err
	})
}

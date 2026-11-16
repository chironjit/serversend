package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

var (
	ErrBootstrapUsed = errors.New("administrator bootstrap is already closed")
	ErrAdminRequired = errors.New("active reauthenticated administrator required")
)

// IssueAccount atomically closes bootstrap or checks live administrator status,
// inserts a hashed temporary credential, and records an audit event.
func (r *Repository) IssueAccount(ctx context.Context, account Account, passwordHash string, expires time.Time, actor protocol.ID, epoch int64) error {
	if account.ID.Validate() != nil || account.CreatedAt.UnixMilli() < 0 || account.CreatedAt.IsZero() || !expires.After(account.CreatedAt) || passwordHash == "" || account.Status != (protocol.AccountStatus{State: protocol.PasswordRequired}) {
		return ErrAuthConflict
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		action := "create_user"
		var actorValue any = actor
		if actor == "" {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
				return err
			}
			if count != 0 || account.Role != "admin" {
				return ErrBootstrapUsed
			}
			action = "bootstrap_admin"
			actorValue = nil
		} else if err := checkAdmin(ctx, tx, actor, epoch); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,username,role,state,password_hash,created_at,temporary_expires_at) VALUES(?,?,?,'password_required',?,?,?)`, account.ID, account.Username, account.Role, passwordHash, account.CreatedAt.UnixMilli(), expires.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,?,?)`, actorValue, account.ID, action, account.CreatedAt.UnixMilli())
		return err
	})
}

func checkAdmin(ctx context.Context, tx *sql.Tx, actor protocol.ID, epoch int64) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE id=? AND role='admin' AND state='active' AND totp_enrolled=1 AND disabled=0 AND auth_epoch=?`, actor, epoch).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAdminRequired
	}
	return nil
}

// RecoverBootstrap is an explicit local-operator operation, never an HTTP
// capability. It can only repair the sole, still-unenrolled initial admin.
func (r *Repository) RecoverBootstrap(ctx context.Context, username, passwordHash string, expires, now time.Time) (protocol.ID, error) {
	if passwordHash == "" || !expires.After(now) {
		return "", ErrAuthConflict
	}
	var id protocol.ID
	err := r.write(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return ErrBootstrapUsed
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE username=? AND role='admin' AND state!='active' AND totp_enrolled=0 AND disabled=0`, username).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrBootstrapUsed
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET password_hash=?,temporary_expires_at=?,state='password_required',credential_revision=credential_revision+1,auth_epoch=auth_epoch+1,failed_attempts=0,blocked_until=0 WHERE id=?`, passwordHash, expires.UnixMilli(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE account_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(NULL,?,'recover_bootstrap',?)`, id, now.UnixMilli())
		return err
	})
	return id, err
}

// ResetPassword and SetDisabled increment auth_epoch in the same transaction as
// the account change. Session authorization must compare its recorded epoch on
// every request; enabling an account never restores earlier authorization.
func (r *Repository) ResetPassword(ctx context.Context, actor protocol.ID, epoch int64, target protocol.ID, passwordHash string, expires, now time.Time) error {
	if target.Validate() != nil || passwordHash == "" || !expires.After(now) {
		return ErrAuthConflict
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := checkAdmin(ctx, tx, actor, epoch); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET password_hash=?,temporary_expires_at=?,state='password_required',credential_revision=credential_revision+1,auth_epoch=auth_epoch+1,failed_attempts=0,blocked_until=0 WHERE id=?`, passwordHash, expires.UnixMilli(), target)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		return invalidateAndAudit(ctx, tx, actor, target, "reset_password", now)
	})
}

func (r *Repository) SetDisabled(ctx context.Context, actor protocol.ID, epoch int64, target protocol.ID, disabled bool, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := checkAdmin(ctx, tx, actor, epoch); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET disabled=?,auth_epoch=auth_epoch+1 WHERE id=?`, disabled, target)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		action := "enable"
		if disabled {
			action = "disable"
		}
		return invalidateAndAudit(ctx, tx, actor, target, action, now)
	})
}

func invalidateAndAudit(ctx context.Context, tx *sql.Tx, actor, target protocol.ID, action string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE account_id=?`, target); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,?,?)`, actor, target, action, now.UnixMilli())
	return err
}

package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

// MaxChallengeFailures bounds wrong second-factor codes per password login.
const MaxChallengeFailures = 5

// TOTPSecret is an encrypted envelope with its replay watermark. The database
// never holds a plaintext secret; opening the envelope belongs to auth.
type TOTPSecret struct {
	Envelope  []byte
	Confirmed bool
	LastStep  int64
}

func (r *Repository) TOTPSecret(ctx context.Context, account protocol.ID) (TOTPSecret, error) {
	var s TOTPSecret
	err := r.db.QueryRowContext(ctx, `SELECT envelope,confirmed,last_step FROM totp_secrets WHERE account_id=?`, account).Scan(&s.Envelope, &s.Confirmed, &s.LastStep)
	return s, err
}

// HasTOTPSecrets lets startup refuse to generate a replacement protection key
// while encrypted secrets that need the lost key are still retained.
func (r *Repository) HasTOTPSecrets(ctx context.Context) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM totp_secrets`).Scan(&count)
	return count != 0, err
}

// liveChallenge rechecks, inside the writing transaction, everything the
// caller observed before its cryptographic work: the unexpired scoped
// challenge, the account snapshot it was issued for and the guessing backoff.
func liveChallenge(ctx context.Context, tx *sql.Tx, ch AuthChallenge, scope string, state protocol.AccountState, now time.Time) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_challenges c JOIN accounts a ON a.id=c.account_id
		WHERE c.token_hash=? AND c.account_id=? AND c.scope=? AND c.expires_at>?
		AND a.credential_revision=c.credential_revision AND a.auth_epoch=c.auth_epoch
		AND a.disabled=0 AND a.state=? AND a.second_factor_blocked_until<=?`,
		ch.TokenHash, ch.AccountID, scope, now.UnixMilli(), state, now.UnixMilli()).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAuthConflict
	}
	return nil
}

func finishSecondFactor(ctx context.Context, tx *sql.Tx, account protocol.ID) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE account_id=?`, account); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET second_factor_failures=0,second_factor_blocked_until=0 WHERE id=?`, account)
	return err
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, account protocol.ID, hashes [][]byte) error {
	if len(hashes) == 0 {
		return ErrAuthConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE account_id=?`, account); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes(account_id,code_hash) VALUES(?,?)`, account, hash); err != nil {
			return err
		}
	}
	return nil
}

// StoreProvisionalTOTP replaces any unconfirmed secret, so an interrupted
// enrollment restarts cleanly. It can never overwrite a confirmed secret.
func (r *Repository) StoreProvisionalTOTP(ctx context.Context, ch AuthChallenge, envelope []byte, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := liveChallenge(ctx, tx, ch, "enroll_totp", protocol.TOTPRequired, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO totp_secrets(account_id,envelope,created_at) VALUES(?,?,?)
			ON CONFLICT(account_id) DO UPDATE SET envelope=excluded.envelope,created_at=excluded.created_at WHERE confirmed=0`, ch.AccountID, envelope, now.UnixMilli())
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		return nil
	})
}

// ActivateTOTP confirms exactly the envelope the caller verified, consumes the
// accepted time step, installs the recovery-code hashes, completes onboarding
// and audits it in one transaction. The enrollment challenge is spent.
func (r *Repository) ActivateTOTP(ctx context.Context, ch AuthChallenge, envelope []byte, step int64, codeHashes [][]byte, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := liveChallenge(ctx, tx, ch, "enroll_totp", protocol.TOTPRequired, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE totp_secrets SET confirmed=1,last_step=? WHERE account_id=? AND confirmed=0 AND envelope=?`, step, ch.AccountID, envelope)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET state='active',totp_enrolled=1 WHERE id=?`, ch.AccountID); err != nil {
			return err
		}
		if err := replaceRecoveryCodes(ctx, tx, ch.AccountID, codeHashes); err != nil {
			return err
		}
		if err := finishSecondFactor(ctx, tx, ch.AccountID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,'enroll_totp',?)`, ch.AccountID, ch.AccountID, now.UnixMilli())
		return err
	})
}

// ConsumeTOTPStep advances the replay watermark and spends the challenge
// atomically. Of any concurrent requests presenting the same code, one wins.
func (r *Repository) ConsumeTOTPStep(ctx context.Context, ch AuthChallenge, envelope []byte, step int64, now time.Time) error {
	return r.consumeTOTPStep(ctx, ch, envelope, step, now, "", nil)
}

// ReplaceRecoveryCodes consumes fresh TOTP and replaces the hashes in the
// same transaction. The current session must still belong to the challenge.
func (r *Repository) ReplaceRecoveryCodes(ctx context.Context, ch AuthChallenge, envelope []byte, step int64, now time.Time, accessHash string, hashes [][]byte) error {
	return r.consumeTOTPStep(ctx, ch, envelope, step, now, accessHash, hashes)
}

func (r *Repository) consumeTOTPStep(ctx context.Context, ch AuthChallenge, envelope []byte, step int64, now time.Time, accessHash string, hashes [][]byte) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := liveChallenge(ctx, tx, ch, "second_factor", protocol.AccountActive, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE totp_secrets SET last_step=? WHERE account_id=? AND confirmed=1 AND envelope=? AND last_step<?`, step, ch.AccountID, envelope, step)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		if hashes != nil {
			session, err := sessionByToken(ctx, tx, accessHash, now)
			if err != nil {
				return err
			}
			if session.AccountID != ch.AccountID || session.Epoch != ch.Epoch || ch.DeviceID != "" {
				return ErrAuthConflict
			}
			if err := replaceRecoveryCodes(ctx, tx, ch.AccountID, hashes); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id) VALUES(?,?,'replace_recovery_codes',?,?)`, ch.AccountID, ch.AccountID, now.UnixMilli(), session.ID); err != nil {
				return err
			}
		}
		return finishSecondFactor(ctx, tx, ch.AccountID)
	})
}

// ConsumeRecoveryCode deletes the matching hash and spends the challenge
// atomically, returning how many unused codes remain.
func (r *Repository) ConsumeRecoveryCode(ctx context.Context, ch AuthChallenge, codeHash []byte, now time.Time) (int, error) {
	var remaining int
	err := r.write(ctx, func(tx *sql.Tx) error {
		if err := liveChallenge(ctx, tx, ch, "second_factor", protocol.AccountActive, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE account_id=? AND code_hash=?`, ch.AccountID, codeHash)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrAuthConflict
		}
		if err := finishSecondFactor(ctx, tx, ch.AccountID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM recovery_codes WHERE account_id=?`, ch.AccountID).Scan(&remaining)
	})
	return remaining, err
}

// RecordSecondFactorFailure applies persistent exponential account backoff,
// bounded at five minutes, and revokes the challenge after too many wrong
// codes. Stale challenges change nothing.
func (r *Repository) RecordSecondFactorFailure(ctx context.Context, ch AuthChallenge, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		var failures int
		if err := tx.QueryRowContext(ctx, `SELECT a.second_factor_failures FROM accounts a JOIN auth_challenges c ON c.account_id=a.id
			WHERE c.token_hash=? AND a.id=? AND a.credential_revision=? AND a.auth_epoch=?`, ch.TokenHash, ch.AccountID, ch.Revision, ch.Epoch).Scan(&failures); err != nil {
			return err
		}
		failures = min(failures+1, 10)
		delay := min(time.Second*time.Duration(1<<uint(failures-1)), 5*time.Minute)
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET second_factor_failures=?,second_factor_blocked_until=? WHERE id=?`, failures, now.Add(delay).UnixMilli(), ch.AccountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE token_hash=? AND failures>=?`, ch.TokenHash, MaxChallengeFailures-1); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE auth_challenges SET failures=failures+1 WHERE token_hash=?`, ch.TokenHash)
		return err
	})
}

// ResetTOTP is the lost-authenticator path. It discards the secret and every
// recovery code, forces re-enrollment, keeps any pending password requirement
// and invalidates existing authorization through auth_epoch.
func (r *Repository) ResetTOTP(ctx context.Context, actor protocol.ID, epoch int64, target protocol.ID, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := checkAdmin(ctx, tx, actor, epoch); err != nil {
			return err
		}
		if err := checkRecovery(ctx, tx, target, "totp"); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET recovery_kind='totp',totp_enrolled=0,state=CASE WHEN state='password_required' THEN state ELSE 'totp_required' END,auth_epoch=auth_epoch+1,second_factor_failures=0,second_factor_blocked_until=0 WHERE id=?`, target)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM totp_secrets WHERE account_id=?`, target); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE account_id=?`, target); err != nil {
			return err
		}
		return invalidateAndAudit(ctx, tx, actor, target, "reset_totp", now)
	})
}

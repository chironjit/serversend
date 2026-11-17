package database

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

const RefreshIdleLifetime = 30 * 24 * time.Hour
const FactorLifetime = 60 * 24 * time.Hour
const AccessLifetime = 10 * time.Minute

var ErrReauthentication = errors.New("authenticator verification required")
var ErrRecoveryPending = errors.New("complete the pending recovery before resetting the other factor")

type RefreshProof struct {
	ID                          protocol.ID
	AccountID                   protocol.ID
	Username, Role              string
	Epoch                       int64
	LastRefresh, LastFactor     time.Time
	Current, Previous, Rotation string
}

func sameHash(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func refreshRow(ctx context.Context, tx *sql.Tx, id protocol.ID, now time.Time) (RefreshProof, error) {
	var p RefreshProof
	var refreshed, factor int64
	err := tx.QueryRowContext(ctx, `SELECT d.id,d.account_id,a.username,a.role,d.auth_epoch,d.last_refresh,d.last_factor,d.refresh_hash,coalesce(d.previous_hash,''),coalesce(d.rotation_id,'')
 FROM device_sessions d JOIN accounts a ON a.id=d.account_id WHERE d.id=? AND d.refresh_hash IS NOT NULL AND d.revoked_at IS NULL AND d.last_refresh>? AND a.auth_epoch=d.auth_epoch AND a.state='active' AND a.totp_enrolled=1 AND a.disabled=0 AND a.role='user'`, id, now.Add(-RefreshIdleLifetime).UnixMilli()).Scan(&p.ID, &p.AccountID, &p.Username, &p.Role, &p.Epoch, &refreshed, &factor, &p.Current, &p.Previous, &p.Rotation)
	p.LastRefresh = time.UnixMilli(refreshed).UTC()
	p.LastFactor = time.UnixMilli(factor).UTC()
	return p, err
}
func proofMatches(p RefreshProof, old, next, rotation string) bool {
	return sameHash(p.Current, old) || (sameHash(p.Previous, old) && sameHash(p.Current, next) && p.Rotation == rotation)
}

// CheckRefresh commits compromise revocation even when authentication is denied.
func (r *Repository) CheckRefresh(ctx context.Context, id protocol.ID, old, next, rotation string, now time.Time) (RefreshProof, error) {
	var p RefreshProof
	var denied bool
	err := r.write(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = refreshRow(ctx, tx, id, now)
		if err != nil {
			return err
		}
		if proofMatches(p, old, next, rotation) {
			return nil
		}
		denied = true
		if sameHash(p.Previous, old) {
			_, err = tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=? WHERE id=?`, now.UnixMilli(), id)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id) VALUES(?,?,'refresh_reuse_revocation',?,?)`, p.AccountID, p.AccountID, now.UnixMilli(), p.ID)
			}
			return err
		}
		return nil
	})
	if err == nil && denied {
		err = ErrAuthConflict
	}
	return p, err
}

// RotateRefresh repeats proof checks inside the write transaction. Exact retries
// issue another short access grant without extending inactivity or TOTP timers.
func (r *Repository) RotateRefresh(ctx context.Context, proof RefreshProof, old, next, rotation, access string, reauth, recovery bool, now time.Time) (RefreshProof, time.Time, error) {
	var expiry time.Time
	var out RefreshProof
	denied := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		p, err := refreshRow(ctx, tx, proof.ID, now)
		if err != nil {
			return err
		}
		if p.Epoch != proof.Epoch || p.AccountID != proof.AccountID {
			return ErrAuthConflict
		}
		if !proofMatches(p, old, next, rotation) {
			denied = true
			if sameHash(p.Previous, old) {
				if _, err = tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=? WHERE id=?`, now.UnixMilli(), p.ID); err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id) VALUES(?,?,'refresh_reuse_revocation',?,?)`, p.AccountID, p.AccountID, now.UnixMilli(), p.ID)
				return err
			}
			return nil
		}
		if !reauth && !p.LastFactor.Add(FactorLifetime).After(now) {
			return ErrReauthentication
		}
		fresh := sameHash(p.Current, old)
		if fresh {
			p.LastRefresh = now
		}
		if reauth {
			p.LastFactor = now
			p.LastRefresh = now
		}
		if fresh {
			if _, err = tx.ExecContext(ctx, `UPDATE device_sessions SET previous_hash=refresh_hash,refresh_hash=?,rotation_id=?,last_refresh=?,last_factor=?,last_seen=? WHERE id=?`, next, rotation, p.LastRefresh.UnixMilli(), p.LastFactor.UnixMilli(), now.UnixMilli(), p.ID); err != nil {
				return err
			}
		} else if reauth {
			if _, err = tx.ExecContext(ctx, `UPDATE device_sessions SET last_factor=?,last_refresh=?,last_seen=? WHERE id=?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), p.ID); err != nil {
				return err
			}
		}
		expiry = now.Add(AccessLifetime)
		if end := p.LastRefresh.Add(RefreshIdleLifetime); end.Before(expiry) {
			expiry = end
		}
		if end := p.LastFactor.Add(FactorLifetime); end.Before(expiry) {
			expiry = end
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM device_access WHERE expires_at<=?`, now.UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO device_access(token_hash,device_id,expires_at) VALUES(?,?,?)`, access, p.ID, expiry.UnixMilli()); err != nil {
			return err
		}
		if reauth {
			action := "device_reauthenticate"
			if recovery {
				action = "device_reauthenticate_recovery"
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,target_id,action,created_at,device_id) VALUES(?,?,?,?,?)`, p.AccountID, p.AccountID, action, now.UnixMilli(), p.ID); err != nil {
				return err
			}
		}
		out = p
		return nil
	})
	if err == nil && denied {
		err = ErrAuthConflict
	}
	return out, expiry, err
}
func (r *Repository) BeginDeviceChallenge(ctx context.Context, p RefreshProof, hash string, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		current, err := refreshRow(ctx, tx, p.ID, now)
		if err != nil {
			return err
		}
		if current.Epoch != p.Epoch || !sameHash(current.Current, p.Current) {
			return ErrAuthConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE device_id=? OR expires_at<=?`, p.ID, now.UnixMilli()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_challenges(token_hash,account_id,scope,credential_revision,auth_epoch,expires_at,device_id) SELECT ?,id,'second_factor',credential_revision,auth_epoch,?,? FROM accounts WHERE id=?`, hash, now.Add(10*time.Minute).UnixMilli(), p.ID, p.AccountID)
		return err
	})
}

func (r *Repository) RevokeByRefresh(ctx context.Context, id protocol.ID, hash string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=? WHERE id=? AND (refresh_hash=? OR previous_hash=?)`, now.UnixMilli(), id, hash, hash)
	return err
}

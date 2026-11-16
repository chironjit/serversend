package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

var authenticationTime = time.Unix(1800000000, 0).UTC()

func authAccount(id protocol.ID, role string) Account {
	a := accountFor(id)
	a.Role = role
	a.CreatedAt = authenticationTime
	return a
}

func authenticationAccounts(t *testing.T) (*Repository, *sql.DB, string, Credentials, Credentials) {
	t.Helper()
	db, dir := openDatabase(t)
	r := NewRepository(db)
	admin := authAccount(ownerA, "admin")
	if err := r.IssueAccount(t.Context(), admin, "admin-encoded-hash", authenticationTime.Add(time.Hour), "", 0); err != nil {
		t.Fatal(err)
	}
	// This fixture begins after mandatory administrator onboarding. Its public
	// service flow is tested by auth; repository tests exercise live DB checks.
	if _, err := db.Exec(`UPDATE accounts SET state='active',totp_enrolled=1,temporary_expires_at=NULL WHERE id=?`, admin.ID); err != nil {
		t.Fatal(err)
	}
	user := authAccount(ownerB, "user")
	if err := r.IssueAccount(t.Context(), user, "temporary-encoded-hash", authenticationTime.Add(time.Hour), admin.ID, 0); err != nil {
		t.Fatal(err)
	}
	adminCredentials, err := r.CredentialsByUsername(t.Context(), admin.Username)
	if err != nil {
		t.Fatal(err)
	}
	userCredentials, err := r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil {
		t.Fatal(err)
	}
	return r, db, dir, adminCredentials, userCredentials
}

func passwordChallenge(c Credentials, serial int) AuthChallenge {
	return AuthChallenge{TokenHash: fmt.Sprintf("%064x", serial), AccountID: c.ID, Scope: "replace_password", Revision: c.Revision, Epoch: c.Epoch, ExpiresAt: authenticationTime.Add(5 * time.Minute)}
}

func authRowCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAuthenticationBootstrapConcurrencyAndAuditAtomicity(t *testing.T) {
	t.Run("concurrent bootstrap has one durable winner", func(t *testing.T) {
		db, dir := openDatabase(t)
		other, err := Open(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for i, handle := range []*sql.DB{db, other} {
			wg.Go(func() {
				<-start
				account := authAccount([]protocol.ID{ownerA, ownerB}[i], "admin")
				results <- NewRepository(handle).IssueAccount(context.Background(), account, "encoded-hash", authenticationTime.Add(time.Hour), "", 0)
			})
		}
		close(start)
		wg.Wait()
		close(results)
		winners, rejected := 0, 0
		for err := range results {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, ErrBootstrapUsed):
				rejected++
			default:
				t.Fatal(err)
			}
		}
		if winners != 1 || rejected != 1 || authRowCount(t, db, `SELECT count(*) FROM accounts`) != 1 || authRowCount(t, db, `SELECT count(*) FROM audit_events WHERE actor_id IS NULL AND action='bootstrap_admin'`) != 1 {
			t.Fatalf("bootstrap winners=%d rejected=%d", winners, rejected)
		}
	})

	t.Run("audit failure rolls back bootstrap and leaves it retryable", func(t *testing.T) {
		db, _ := openDatabase(t)
		r := NewRepository(db)
		if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'injected audit write failure'); END`); err != nil {
			t.Fatal(err)
		}
		a := authAccount(ownerA, "admin")
		if err := r.IssueAccount(t.Context(), a, "encoded-hash", authenticationTime.Add(time.Hour), "", 0); err == nil {
			t.Fatal("audit failure ignored")
		}
		if authRowCount(t, db, `SELECT count(*) FROM accounts`) != 0 {
			t.Fatal("unaudited bootstrap account persisted")
		}
		if _, err := db.Exec(`DROP TRIGGER reject_audit`); err != nil {
			t.Fatal(err)
		}
		if err := r.IssueAccount(t.Context(), a, "encoded-hash", authenticationTime.Add(time.Hour), "", 0); err != nil {
			t.Fatalf("rolled-back bootstrap could not be retried: %v", err)
		}
	})
}

func TestAuthenticationAdministrationUsesLivePrivileges(t *testing.T) {
	for _, scenario := range []string{"non-admin", "stale epoch", "disabled admin", "onboarding admin"} {
		t.Run(scenario, func(t *testing.T) {
			r, db, _, admin, user := authenticationAccounts(t)
			actor, epoch := admin.ID, admin.Epoch
			switch scenario {
			case "non-admin":
				actor = user.ID
			case "stale epoch":
				epoch++
			case "disabled admin":
				if _, err := db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, actor); err != nil {
					t.Fatal(err)
				}
			case "onboarding admin":
				if _, err := db.Exec(`UPDATE accounts SET state='password_required' WHERE id=?`, actor); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ResetPassword(t.Context(), actor, epoch, user.ID, "new-hash", authenticationTime.Add(time.Hour), authenticationTime); !errors.Is(err, ErrAdminRequired) {
				t.Fatalf("unauthorized password reset: %v", err)
			}
			if err := r.SetDisabled(t.Context(), actor, epoch, user.ID, true, authenticationTime); !errors.Is(err, ErrAdminRequired) {
				t.Fatalf("unauthorized disablement: %v", err)
			}
			a := authAccount(uploadID, "user")
			if err := r.IssueAccount(t.Context(), a, "hash", authenticationTime.Add(time.Hour), actor, epoch); !errors.Is(err, ErrAdminRequired) {
				t.Fatalf("unauthorized account creation: %v", err)
			}
			got, err := r.CredentialsByUsername(t.Context(), user.Username)
			if err != nil || got.PasswordHash != user.PasswordHash || got.Epoch != user.Epoch || got.Status.Disabled {
				t.Fatalf("denied administration modified target: %v", err)
			}
		})
	}
}

func TestAuthenticationResetAndDisableInvalidationAreAtomic(t *testing.T) {
	for _, operation := range []string{"reset", "disable"} {
		t.Run(operation, func(t *testing.T) {
			r, db, _, admin, user := authenticationAccounts(t)
			ch := passwordChallenge(user, 1)
			if err := r.BeginChallenge(t.Context(), user, ch, authenticationTime); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'injected audit write failure'); END`); err != nil {
				t.Fatal(err)
			}
			change := func() error {
				if operation == "reset" {
					return r.ResetPassword(t.Context(), admin.ID, admin.Epoch, user.ID, "reset-hash", authenticationTime.Add(time.Hour), authenticationTime)
				}
				return r.SetDisabled(t.Context(), admin.ID, admin.Epoch, user.ID, true, authenticationTime)
			}
			if err := change(); err == nil {
				t.Fatal("audit failure ignored")
			}
			got, err := r.CredentialsByUsername(t.Context(), user.Username)
			if err != nil || got.PasswordHash != user.PasswordHash || got.Revision != user.Revision || got.Epoch != user.Epoch || got.Status != user.Status {
				t.Fatalf("failed administration partially changed account: %v", err)
			}
			if _, _, err := r.Challenge(t.Context(), ch.TokenHash, authenticationTime); err != nil {
				t.Fatalf("failed administration discarded usable challenge: %v", err)
			}
			if _, err := db.Exec(`DROP TRIGGER reject_audit`); err != nil {
				t.Fatal(err)
			}
			if err := change(); err != nil {
				t.Fatal(err)
			}
			got, err = r.CredentialsByUsername(t.Context(), user.Username)
			if err != nil || got.Epoch != user.Epoch+1 {
				t.Fatalf("administration did not advance revocation epoch: %v", err)
			}
			if authRowCount(t, db, `SELECT count(*) FROM auth_challenges WHERE account_id=?`, user.ID) != 0 {
				t.Fatal("administration retained restricted authorization")
			}
			if err := r.ReplacePassword(t.Context(), ch, "stale-password", authenticationTime); !errors.Is(err, ErrAuthConflict) {
				t.Fatalf("stale challenge survived administration: %v", err)
			}
			if operation == "disable" {
				if err := r.SetDisabled(t.Context(), admin.ID, admin.Epoch, user.ID, false, authenticationTime); err != nil {
					t.Fatal(err)
				}
				if _, _, err := r.Challenge(t.Context(), ch.TokenHash, authenticationTime); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("enablement restored discarded authorization")
				}
			}
		})
	}
}

func TestAuthenticationChallengeBoundsAndStaleSnapshots(t *testing.T) {
	r, _, _, admin, user := authenticationAccounts(t)
	for _, scenario := range []string{"expired", "overlong", "foreign account", "wrong revision", "wrong epoch", "wrong scope"} {
		t.Run(scenario, func(t *testing.T) {
			ch := passwordChallenge(user, 1)
			switch scenario {
			case "expired":
				ch.ExpiresAt = authenticationTime
			case "overlong":
				ch.ExpiresAt = authenticationTime.Add(10*time.Minute + time.Millisecond)
			case "foreign account":
				ch.AccountID = admin.ID
			case "wrong revision":
				ch.Revision++
			case "wrong epoch":
				ch.Epoch++
			case "wrong scope":
				ch.Scope = "second_factor"
			}
			if err := r.BeginChallenge(t.Context(), user, ch, authenticationTime); !errors.Is(err, ErrAuthConflict) {
				t.Fatalf("invalid challenge accepted: %v", err)
			}
		})
	}
	ch := passwordChallenge(user, 2)
	ch.ExpiresAt = authenticationTime.Add(10 * time.Minute)
	if err := r.BeginChallenge(t.Context(), user, ch, authenticationTime); err != nil {
		t.Fatalf("maximum challenge lifetime rejected: %v", err)
	}
	if _, _, err := r.Challenge(t.Context(), ch.TokenHash, ch.ExpiresAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("challenge usable at expiry boundary: %v", err)
	}
	if err := r.ReplacePassword(t.Context(), ch, "permanent-hash", ch.ExpiresAt); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("expired challenge replaced password: %v", err)
	}
	if err := r.ResetPassword(t.Context(), admin.ID, admin.Epoch, user.ID, "reset-hash", authenticationTime.Add(time.Hour), authenticationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginChallenge(t.Context(), user, passwordChallenge(user, 3), authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("pre-reset password-verification snapshot survived reset: %v", err)
	}
	if err := r.RecordPasswordFailure(t.Context(), user, authenticationTime); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale password failure should not penalize reset: %v", err)
	}
}

func TestStalePasswordFailureCannotReapplyBackoffAfterDisableEnable(t *testing.T) {
	r, _, _, admin, user := authenticationAccounts(t)
	if err := r.SetDisabled(t.Context(), admin.ID, admin.Epoch, user.ID, true, authenticationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.SetDisabled(t.Context(), admin.ID, admin.Epoch, user.ID, false, authenticationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPasswordFailure(t.Context(), user, authenticationTime); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale failed request crossed revocation epoch: %v", err)
	}
	current, err := r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil || current.FailedAttempts != 0 || current.BlockedUntil != 0 {
		t.Fatal("stale attempt reapplied account backoff")
	}
}

func TestAuthenticationChallengeReplacementIsAtomic(t *testing.T) {
	r, db, dir, _, user := authenticationAccounts(t)
	first := passwordChallenge(user, 1)
	if err := r.BeginChallenge(t.Context(), user, first, authenticationTime); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_challenge BEFORE INSERT ON auth_challenges BEGIN SELECT RAISE(ABORT,'injected challenge write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginChallenge(t.Context(), user, passwordChallenge(user, 2), authenticationTime); err == nil {
		t.Fatal("challenge persistence failure ignored")
	}
	if _, _, err := r.Challenge(t.Context(), first.TokenHash, authenticationTime); err != nil {
		t.Fatalf("failed challenge replacement consumed prior authorization: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_challenge`); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	challenges := []AuthChallenge{passwordChallenge(user, 3), passwordChallenge(user, 4)}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, repo := range []*Repository{r, NewRepository(other)} {
		wg.Go(func() { results <- repo.BeginChallenge(context.Background(), user, challenges[i], authenticationTime) })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if authRowCount(t, db, `SELECT count(*) FROM auth_challenges WHERE account_id=?`, user.ID) != 1 {
		t.Fatal("concurrent password verifications left multiple restricted attempts")
	}
	usable := 0
	for _, ch := range append(challenges, first) {
		if _, _, err := r.Challenge(t.Context(), ch.TokenHash, authenticationTime); err == nil {
			usable++
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if usable != 1 {
		t.Fatalf("concurrent challenge replacement left %d usable tokens", usable)
	}
}

func TestAuthenticationTemporaryCredentialExpiryIsLive(t *testing.T) {
	r, db, _, _, user := authenticationAccounts(t)
	ch := passwordChallenge(user, 1)
	if err := r.BeginChallenge(t.Context(), user, ch, authenticationTime); err != nil {
		t.Fatal(err)
	}
	// A restricted challenge must not extend the underlying credential's
	// expiry. Test a live expiry change while that challenge is outstanding.
	expires := authenticationTime.Add(2 * time.Minute)
	if _, err := db.Exec(`UPDATE accounts SET temporary_expires_at=? WHERE id=?`, expires.UnixMilli(), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplacePassword(t.Context(), ch, "permanent-hash", expires); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("outstanding challenge extended temporary password expiry: %v", err)
	}
	ch.ExpiresAt = expires.Add(5 * time.Minute)
	if err := r.BeginChallenge(t.Context(), user, ch, expires); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("credential accepted at its exact expiry boundary: %v", err)
	}
}

func TestAuthenticationReplacementRaceAndAuditRollback(t *testing.T) {
	r, db, dir, _, user := authenticationAccounts(t)
	ch := passwordChallenge(user, 1)
	if err := r.BeginChallenge(t.Context(), user, ch, authenticationTime); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'injected audit write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplacePassword(t.Context(), ch, "permanent-hash", authenticationTime); err == nil {
		t.Fatal("replacement audit failure ignored")
	}
	if _, _, err := r.Challenge(t.Context(), ch.TokenHash, authenticationTime); err != nil {
		t.Fatalf("failed replacement consumed challenge: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_audit`); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, repo := range []*Repository{r, NewRepository(other)} {
		wg.Go(func() {
			results <- repo.ReplacePassword(context.Background(), ch, "permanent-hash", authenticationTime)
		})
	}
	wg.Wait()
	close(results)
	winners, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrAuthConflict):
			rejected++
		default:
			t.Fatal(err)
		}
	}
	got, err := r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil || got.PasswordHash != "permanent-hash" || got.Status.State != protocol.TOTPRequired || got.Revision != 1 || got.Epoch != 1 || got.TemporaryExpiresAt.Valid {
		t.Fatalf("invalid replacement result: %v", err)
	}
	if winners != 1 || rejected != 1 || authRowCount(t, db, `SELECT count(*) FROM audit_events WHERE action='replace_password' AND target_id=?`, user.ID) != 1 {
		t.Fatalf("replacement winners=%d rejected=%d", winners, rejected)
	}
}

func TestAuthenticationBackoffIsBoundedAndPersistent(t *testing.T) {
	r, db, dir, _, user := authenticationAccounts(t)
	var previousDelay int64
	for i := range 16 {
		if err := r.RecordPasswordFailure(t.Context(), user, authenticationTime); err != nil {
			t.Fatal(err)
		}
		current, err := r.CredentialsByUsername(t.Context(), user.Username)
		if err != nil {
			t.Fatal(err)
		}
		delay := current.BlockedUntil - authenticationTime.UnixMilli()
		if current.FailedAttempts < 1 || current.FailedAttempts > 10 || delay < previousDelay || delay > (5*time.Minute).Milliseconds() {
			t.Fatal("persistent backoff count or delay exceeds its bounded policy")
		}
		if i == 0 && delay != time.Second.Milliseconds() {
			t.Fatal("initial password failure did not apply a one-second backoff")
		}
		previousDelay = delay
	}
	if err := r.BeginChallenge(t.Context(), user, passwordChallenge(user, 1), authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("backoff bypassed by successful credential snapshot: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r = NewRepository(reopened)
	current, err := r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil || current.FailedAttempts != 10 || current.BlockedUntil != authenticationTime.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("restart changed backoff policy: %v", err)
	}
	now := authenticationTime.Add(5 * time.Minute)
	ch := passwordChallenge(current, 2)
	ch.ExpiresAt = now.Add(5 * time.Minute)
	if err := r.BeginChallenge(t.Context(), current, ch, now); err != nil {
		t.Fatalf("login rejected at backoff boundary: %v", err)
	}
	current, err = r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil || current.FailedAttempts != 0 || current.BlockedUntil != 0 {
		t.Fatalf("successful verification did not clear backoff: %v", err)
	}
}

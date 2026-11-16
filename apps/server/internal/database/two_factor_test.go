package database

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func enrollmentChallenge(t *testing.T, r *Repository, db *sql.DB, c Credentials) AuthChallenge {
	t.Helper()
	if _, err := db.Exec(`UPDATE accounts SET state='totp_required',totp_enrolled=0,temporary_expires_at=NULL WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err := r.CredentialsByUsername(t.Context(), c.Username)
	if err != nil {
		t.Fatal(err)
	}
	ch := passwordChallenge(c, 1)
	ch.Scope = "enroll_totp"
	if err := r.BeginChallenge(t.Context(), c, ch, authenticationTime); err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestTwoFactorTransitionsAreAtomicAndScoped(t *testing.T) {
	r, db, _, _, user := authenticationAccounts(t)
	ch := enrollmentChallenge(t, r, db, user)
	first, second := bytes.Repeat([]byte{1}, 61), bytes.Repeat([]byte{2}, 61)
	hashes := [][]byte{bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)}

	if err := r.StoreProvisionalTOTP(t.Context(), ch, []byte("short"), authenticationTime); err == nil {
		t.Fatal("malformed envelope stored")
	}
	foreign := ch
	foreign.AccountID = ownerA
	if err := r.StoreProvisionalTOTP(t.Context(), foreign, first, authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatalf("challenge stored a secret for another account: %v", err)
	}
	if err := r.StoreProvisionalTOTP(t.Context(), ch, first, ch.ExpiresAt); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("expired challenge stored a secret")
	}
	for _, envelope := range [][]byte{first, second} {
		if err := r.StoreProvisionalTOTP(t.Context(), ch, envelope, authenticationTime); err != nil {
			t.Fatal(err)
		}
	}
	// Activation is bound to the verified envelope and rolls back as a unit.
	if err := r.ActivateTOTP(t.Context(), ch, first, 5, hashes, authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("replaced provisional envelope activated")
	}
	if err := r.ActivateTOTP(t.Context(), ch, second, 5, [][]byte{hashes[0], hashes[0]}, authenticationTime); err == nil {
		t.Fatal("duplicate recovery hashes accepted")
	}
	if err := r.ActivateTOTP(t.Context(), ch, second, 5, nil, authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("enrollment activated without recovery codes")
	}
	stored, err := r.TOTPSecret(t.Context(), user.ID)
	if err != nil || stored.Confirmed || stored.LastStep != -1 || authRowCount(t, db, `SELECT count(*) FROM recovery_codes`) != 0 || authRowCount(t, db, `SELECT count(*) FROM accounts WHERE id=? AND state='totp_required'`, user.ID) != 1 {
		t.Fatalf("failed activation left partial state: %+v %v", stored, err)
	}
	if err := r.ActivateTOTP(t.Context(), ch, second, 5, hashes, authenticationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.StoreProvisionalTOTP(t.Context(), ch, first, authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("confirmed secret overwritten")
	}

	active, err := r.CredentialsByUsername(t.Context(), user.Username)
	if err != nil {
		t.Fatal(err)
	}
	login := passwordChallenge(active, 2)
	login.Scope = "second_factor"
	if err := r.BeginChallenge(t.Context(), active, login, authenticationTime); err != nil {
		t.Fatal(err)
	}
	for _, step := range []int64{5, 4} {
		if err := r.ConsumeTOTPStep(t.Context(), login, second, step, authenticationTime); !errors.Is(err, ErrAuthConflict) {
			t.Fatalf("replayed step %d consumed", step)
		}
	}
	if _, err := r.ConsumeRecoveryCode(t.Context(), login, bytes.Repeat([]byte{9}, 32), authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("unknown recovery hash consumed")
	}
	// A failure blocks consumption until its backoff passes; stale work is inert.
	if err := r.RecordSecondFactorFailure(t.Context(), login, authenticationTime); err != nil {
		t.Fatal(err)
	}
	stale := login
	stale.Epoch++
	if err := r.RecordSecondFactorFailure(t.Context(), stale, authenticationTime); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale failure recorded: %v", err)
	}
	if err := r.ConsumeTOTPStep(t.Context(), login, second, 6, authenticationTime); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("step consumed during backoff")
	}
	later := authenticationTime.Add(time.Second)
	if remaining, err := r.ConsumeRecoveryCode(t.Context(), login, hashes[0], later); err != nil || remaining != 1 {
		t.Fatalf("recovery consumption: %d %v", remaining, err)
	}
	if err := r.ConsumeTOTPStep(t.Context(), login, second, 6, later); !errors.Is(err, ErrAuthConflict) {
		t.Fatal("spent challenge consumed a step")
	}
	if has, err := r.HasTOTPSecrets(t.Context()); err != nil || !has {
		t.Fatal("retained secret not reported")
	}
}

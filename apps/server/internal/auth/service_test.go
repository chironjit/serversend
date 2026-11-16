package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/testutil"
)

// This test double exercises durable state, never password cryptography. The
// production Argon2id implementation has a separate suite and resource budget.
type stateTestHasher struct{}

func (stateTestHasher) Hash(password string) (string, error) {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:]), nil
}
func (h stateTestHasher) Verify(hash, password string) bool {
	want, _ := h.Hash(password)
	return hash == want
}

func authFixture(t *testing.T) (*Service, *sql.DB, *testutil.Clock, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := testutil.NewClock(time.Unix(1800000000, 0))
	s, err := New(database.NewRepository(db), stateTestHasher{}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, clock, dir
}

func bootstrapFixture(t *testing.T, s *Service) IssuedPassword {
	t.Helper()
	issued, err := s.BootstrapAdmin(t.Context(), "Admin")
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestBootstrapOneTimeAndPasswordReplacement(t *testing.T) {
	s, db, _, _ := authFixture(t)
	issued := bootstrapFixture(t, s)
	if issued.Username != "admin" || !validToken(issued.TemporaryPassword) {
		t.Fatal("bootstrap did not issue a canonical high-entropy temporary credential")
	}
	if _, err := s.BootstrapAdmin(t.Context(), "another"); !errors.Is(err, database.ErrBootstrapUsed) {
		t.Fatalf("bootstrap reused: %v", err)
	}
	attempt, err := s.AuthenticatePassword(t.Context(), "ADMIN", issued.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.NextStep != "replace_password" || !validToken(attempt.Challenge) {
		t.Fatal("temporary login escaped restricted scope")
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "long permanent password"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "another permanent password"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("password challenge reused: %v", err)
	}
	current, err := s.repo.CredentialsByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != (protocol.AccountStatus{State: protocol.TOTPRequired}) || current.TemporaryExpiresAt.Valid || current.Epoch != 1 {
		t.Fatal("password replacement skipped TOTP or retained temporary credential")
	}
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("spent temporary password accepted")
	}
	var storedHash string
	var count int
	if err := db.QueryRow(`SELECT password_hash FROM accounts`).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == issued.TemporaryPassword || storedHash == "long permanent password" {
		t.Fatal("plaintext password stored")
	}
	if err := db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("bootstrap and replacement not audited: %d %v", count, err)
	}
}

func TestPasswordExpiryBackoffAndRestart(t *testing.T) {
	s, db, clock, dir := authFixture(t)
	issued := bootstrapFixture(t, s)
	if _, err := s.AuthenticatePassword(t.Context(), "admin", "wrong"); !errors.Is(err, ErrCredentials) {
		t.Fatal(err)
	}
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("account backoff ignored")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s, err = New(database.NewRepository(reopened), stateTestHasher{}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("restart reset backoff")
	}
	clock.Advance(time.Second)
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); err != nil {
		t.Fatal(err)
	}
	clock.Advance(TemporaryPasswordLifetime - time.Second)
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("temporary password accepted at expiry boundary")
	}
}

func TestRestrictedChallengeExpiryScopeAndConcurrentReplacement(t *testing.T) {
	s, _, clock, _ := authFixture(t)
	issued := bootstrapFixture(t, s)
	attempt, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(ChallengeLifetime)
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "a strong permanent password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired challenge accepted")
	}
	attempt, err = s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			results <- s.ReplacePassword(context.Background(), attempt.Challenge, "a strong permanent password")
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrCredentials) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("password replacement had %d winners", wins)
	}
	attempt, err = s.AuthenticatePassword(t.Context(), "admin", "a strong permanent password")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.NextStep != "enroll_totp" {
		t.Fatal("interrupted enrollment cannot resume")
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "a different strong password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("enrollment challenge used to replace password")
	}
}

func activeActor(t *testing.T, s *Service, db *sql.DB, id protocol.ID) Principal {
	t.Helper()
	if _, err := db.Exec(`UPDATE accounts SET state='active',totp_enrolled=1,temporary_expires_at=NULL WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return Principal{accountID: id, epoch: 0, reauthenticatedAt: s.now()}
}

func TestAdminPermissionsResetsAndDisablement(t *testing.T) {
	s, db, clock, _ := authFixture(t)
	admin := bootstrapFixture(t, s)
	if _, err := s.CreateUser(t.Context(), Principal{}, "user"); !errors.Is(err, ErrForbidden) {
		t.Fatal("onboarding attempt gained administrator access")
	}
	actor := activeActor(t, s, db, admin.AccountID)
	user, err := s.CreateUser(t.Context(), actor, "User")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(t.Context(), actor, "USER"); err == nil {
		t.Fatal("duplicate case-insensitive user accepted")
	}
	nonAdmin := Principal{accountID: user.AccountID, epoch: 0, reauthenticatedAt: clock.Now()}
	if _, err := s.CreateUser(t.Context(), nonAdmin, "foreign"); !errors.Is(err, database.ErrAdminRequired) {
		t.Fatalf("non-admin created an account: %v", err)
	}
	attempt, err := s.AuthenticatePassword(t.Context(), "user", user.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(t.Context(), actor, user.AccountID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "a strong permanent password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("disablement did not invalidate challenge")
	}
	if err := s.SetDisabled(t.Context(), actor, user.AccountID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "a strong permanent password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("enablement revived a challenge")
	}
	// An administrator password reset must preserve existing TOTP enrollment.
	if _, err := db.Exec(`UPDATE accounts SET state='active',totp_enrolled=1 WHERE id=?`, user.AccountID); err != nil {
		t.Fatal(err)
	}
	reset, err := s.ResetPassword(t.Context(), actor, user.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.repo.CredentialsByUsername(t.Context(), "user")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Status.TOTPEnrolled || c.Status.State != protocol.PasswordRequired || c.Epoch != 3 {
		t.Fatal("reset lost TOTP or failed epoch revocation")
	}
	clock.Advance(time.Second)
	attempt, err = s.AuthenticatePassword(t.Context(), "user", reset.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePassword(t.Context(), attempt.Challenge, "new strong permanent password"); err != nil {
		t.Fatal(err)
	}
	attempt, err = s.AuthenticatePassword(t.Context(), "user", "new strong permanent password")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.NextStep != "second_factor" {
		t.Fatal("password reset bypassed required existing second factor")
	}
	clock.Advance(5*time.Minute + time.Millisecond)
	if _, err := s.ResetPassword(t.Context(), actor, user.AccountID); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale reauthentication accepted")
	}
}

func TestMalformedPasswordsUsernamesAndGlobalLimit(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat(" ", 12), strings.Repeat("a", 129), "password\x00control", string([]byte{0xff})} {
		if ValidatePassword(password) == nil {
			t.Fatalf("weak or malformed password accepted")
		}
	}
	if ValidatePassword("a long Unicode 密碼 passphrase") != nil {
		t.Fatal("Unicode passphrase rejected")
	}
	s, _, _, _ := authFixture(t)
	for _, username := range []string{"", " user ", "name@example.com", "\x00", "東京", "Kelvin"} {
		if _, err := s.BootstrapAdmin(t.Context(), username); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid username accepted: %v", err)
		}
	}
	for range 10 {
		if _, err := s.AuthenticatePassword(t.Context(), "missing", "anything"); !errors.Is(err, ErrCredentials) {
			t.Fatal(err)
		}
	}
	if _, err := s.AuthenticatePassword(t.Context(), "new-missing-name", "anything"); !errors.Is(err, ErrOverloaded) {
		t.Fatal("global limit can be bypassed using new usernames")
	}
}

func TestLocalBootstrapRecoveryOnlyRepairsIncompleteAdministrator(t *testing.T) {
	s, db, clock, _ := authFixture(t)
	issued := bootstrapFixture(t, s)
	oldAttempt, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(TemporaryPasswordLifetime)
	recovered, err := s.RecoverBootstrapAdmin(t.Context(), "admin")
	if err != nil || recovered.AccountID != issued.AccountID || recovered.TemporaryPassword == issued.TemporaryPassword {
		t.Fatal("local bootstrap recovery failed")
	}
	if err := s.ReplacePassword(t.Context(), oldAttempt.Challenge, "a permanent long password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("bootstrap recovery did not invalidate old challenge")
	}
	if _, err := s.AuthenticatePassword(t.Context(), "admin", issued.TemporaryPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("old temporary password survived recovery")
	}
	clock.Advance(time.Second)
	if _, err := s.AuthenticatePassword(t.Context(), "admin", recovered.TemporaryPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE accounts SET state='active',totp_enrolled=1 WHERE id=?`, issued.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverBootstrapAdmin(t.Context(), "admin"); !errors.Is(err, database.ErrBootstrapUsed) {
		t.Fatal("bootstrap recovery reset an active administrator")
	}
}

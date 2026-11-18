package auth

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth/twofactor"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/testutil"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

const (
	twoFactorServerID = protocol.ID("0123456789abcdef0123456789abcdef")
	fixturePassword   = "a long permanent password"
)

func twoFactorService(t *testing.T, db *sql.DB, clock *testutil.Clock, keyByte byte) *Service {
	t.Helper()
	protector, err := twofactor.NewSecretProtector(bytes.Repeat([]byte{keyByte}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(database.NewRepository(db), stateTestHasher{}, clock.Now, WithTwoFactor(twoFactorServerID, protector))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func twoFactorFixture(t *testing.T) (*Service, *sql.DB, *testutil.Clock) {
	t.Helper()
	_, db, clock, _ := authFixture(t)
	return twoFactorService(t, db, clock, 7), db, clock
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := hotp.GenerateCodeCustom(secret, uint64(at.Unix()/30), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func wrongCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func login(t *testing.T, s *Service, username, password, wantStep string) string {
	t.Helper()
	attempt, err := s.AuthenticatePassword(t.Context(), username, password)
	if err != nil || attempt.NextStep != wantStep {
		t.Fatalf("login step=%q want %q: %v", attempt.NextStep, wantStep, err)
	}
	return attempt.Challenge
}

// onboard completes mandatory password replacement and TOTP enrollment, then
// moves the clock to a fresh time step with a refilled attempt budget.
func onboard(t *testing.T, s *Service, clock *testutil.Clock, username, temporary string) (string, []string) {
	t.Helper()
	if err := s.ReplacePassword(t.Context(), login(t, s, username, temporary, "replace_password"), fixturePassword); err != nil {
		t.Fatal(err)
	}
	token := login(t, s, username, fixturePassword, "enroll_totp")
	enrollment, err := s.BeginTOTPEnrollment(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.ConfirmTOTPEnrollment(t.Context(), token, codeAt(t, enrollment.Secret, clock.Now()))
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	return enrollment.Secret, codes
}

func principalFor(t *testing.T, s *Service, username string) Principal {
	t.Helper()
	c, err := s.repo.CredentialsByUsername(t.Context(), username)
	if err != nil {
		t.Fatal(err)
	}
	return Principal{accountID: c.ID, epoch: c.Epoch, reauthenticatedAt: s.now()}
}

func TestTOTPEnrollmentInterruptionActivationAndReplay(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	issued := bootstrapFixture(t, s)
	temporary := login(t, s, "admin", issued.TemporaryPassword, "replace_password")
	if _, err := s.BeginTOTPEnrollment(t.Context(), temporary); !errors.Is(err, ErrCredentials) {
		t.Fatalf("temporary-password challenge enrolled TOTP: %v", err)
	}
	if err := s.ReplacePassword(t.Context(), temporary, fixturePassword); err != nil {
		t.Fatal(err)
	}
	// Administrator onboarding has no exemption: no admin action before TOTP.
	if _, err := s.CreateUser(t.Context(), principalFor(t, s, "admin"), "early"); !errors.Is(err, database.ErrAdminRequired) {
		t.Fatalf("unenrolled administrator acted: %v", err)
	}
	token := login(t, s, "admin", fixturePassword, "enroll_totp")
	if _, err := s.ConfirmTOTPEnrollment(t.Context(), token, "123456"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("confirmation without provisional secret: %v", err)
	}
	if _, err := s.VerifySecondFactor(t.Context(), token, "123456"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("enrollment challenge accepted as login: %v", err)
	}
	abandoned, err := s.BeginTOTPEnrollment(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(abandoned.URI, "otpauth://totp/ServerSend:admin?") || !bytes.HasPrefix(abandoned.QRPNG, []byte("\x89PNG")) {
		t.Fatal("enrollment material is incomplete")
	}
	// An interrupted enrollment restarts with a new secret, also after restart.
	clock.Advance(30 * time.Second)
	s = twoFactorService(t, db, clock, 7)
	enrollment, err := s.BeginTOTPEnrollment(t.Context(), token)
	if err != nil || enrollment.Secret == abandoned.Secret {
		t.Fatalf("enrollment restart did not replace the secret: %v", err)
	}
	if _, err := s.ConfirmTOTPEnrollment(t.Context(), token, codeAt(t, abandoned.Secret, clock.Now())); !errors.Is(err, ErrCredentials) {
		t.Fatalf("abandoned secret confirmed: %v", err)
	}
	good := codeAt(t, enrollment.Secret, clock.Now())
	if _, err := s.ConfirmTOTPEnrollment(t.Context(), token, good); !errors.Is(err, ErrCredentials) {
		t.Fatal("second-factor backoff ignored during enrollment")
	}
	if c, _ := s.repo.CredentialsByUsername(t.Context(), "admin"); c.Status.State != protocol.TOTPRequired {
		t.Fatal("failed confirmation changed account state")
	}
	clock.Advance(time.Second)
	codes, err := s.ConfirmTOTPEnrollment(t.Context(), token, good)
	if err != nil || len(codes) != twofactor.RecoveryCodeCount {
		t.Fatalf("confirmation: %d codes, %v", len(codes), err)
	}
	if _, err := s.ConfirmTOTPEnrollment(t.Context(), token, good); !errors.Is(err, ErrCredentials) {
		t.Fatal("spent enrollment challenge reused")
	}
	c, err := s.repo.CredentialsByUsername(t.Context(), "admin")
	if err != nil || c.Status != (protocol.AccountStatus{State: protocol.AccountActive, TOTPEnrolled: true}) || c.SecondFactorBlockedUntil != 0 {
		t.Fatalf("account not activated: %+v %v", c, err)
	}

	// Only the envelope and hashes are stored.
	var envelope []byte
	var confirmed bool
	if err := db.QueryRow(`SELECT envelope,confirmed FROM totp_secrets`).Scan(&envelope, &confirmed); err != nil || !confirmed || bytes.Contains(envelope, []byte(enrollment.Secret)) {
		t.Fatalf("stored secret is not a confirmed envelope: %v", err)
	}
	rows, err := db.Query(`SELECT code_hash FROM recovery_codes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := 0
	for rows.Next() {
		var hash []byte
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		stored++
		for _, code := range codes {
			if bytes.Contains(hash, []byte(code)) || len(hash) != 32 {
				t.Fatal("recovery code stored in plaintext")
			}
		}
	}
	if stored != len(codes) {
		t.Fatalf("stored %d recovery hashes", stored)
	}
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='enroll_totp' AND actor_id=target_id`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("enrollment not audited: %d %v", audits, err)
	}

	// The enrollment code consumed its time step; the next step authenticates once.
	if _, err := s.BeginTOTPEnrollment(t.Context(), login(t, s, "admin", fixturePassword, "second_factor")); !errors.Is(err, ErrCredentials) {
		t.Fatal("active account re-enrolled without a reset")
	}
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), good); !errors.Is(err, ErrCredentials) {
		t.Fatal("enrollment code replayed as login")
	}
	clock.Advance(30 * time.Second)
	token = login(t, s, "admin", fixturePassword, "second_factor")
	next := codeAt(t, enrollment.Secret, clock.Now())
	result, err := s.VerifySecondFactor(t.Context(), token, next)
	if err != nil || result.AccountID != issued.AccountID || result.Epoch != c.Epoch || result.UsedRecoveryCode {
		t.Fatalf("second factor: %+v %v", result, err)
	}
	if _, err := s.VerifySecondFactor(t.Context(), token, next); !errors.Is(err, ErrCredentials) {
		t.Fatal("spent second-factor challenge reused")
	}
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), next); !errors.Is(err, ErrCredentials) {
		t.Fatal("consumed time step replayed with a new challenge")
	}
}

func TestSecondFactorSkewExpiryAndConcurrentUse(t *testing.T) {
	s, _, clock := twoFactorFixture(t)
	secret, _ := onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)

	// A code accepted early through skew also consumes that future step.
	future := codeAt(t, secret, clock.Now().Add(30*time.Second))
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), future); err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), future); !errors.Is(err, ErrCredentials) {
		t.Fatal("future-skew code replayed when the clock caught up")
	}
	clock.Advance(30 * time.Second)
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now().Add(-61*time.Second))); !errors.Is(err, ErrCredentials) {
		t.Fatal("code outside the skew window accepted")
	}

	clock.Advance(time.Minute)
	token := login(t, s, "admin", fixturePassword, "second_factor")
	clock.Advance(ChallengeLifetime)
	if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired second-factor challenge accepted")
	}

	token = login(t, s, "admin", fixturePassword, "second_factor")
	code := codeAt(t, secret, clock.Now())
	results := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			_, err := s.VerifySecondFactor(t.Context(), token, code)
			results <- err
		}()
	}
	start.Done()
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("concurrent identical codes: %v / %v", first, second)
	}
}

func TestRecoveryCodesAreSingleUseAndReplaceable(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	secret, codes := onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)

	token := login(t, s, "admin", fixturePassword, "second_factor")
	if _, err := s.VerifySecondFactor(t.Context(), token, strings.Repeat("0", 32)); !errors.Is(err, ErrCredentials) {
		t.Fatal("unknown recovery code accepted")
	}
	clock.Advance(time.Second)
	compact := " " + strings.ToLower(strings.ReplaceAll(codes[0], "-", "")) + " "
	result, err := s.VerifySecondFactor(t.Context(), token, compact)
	if err != nil || !result.UsedRecoveryCode || result.RecoveryCodesRemaining != len(codes)-1 {
		t.Fatalf("recovery login: %+v %v", result, err)
	}
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), codes[0]); !errors.Is(err, ErrCredentials) {
		t.Fatal("recovery code reused")
	}

	clock.Advance(time.Minute)
	token = login(t, s, "admin", fixturePassword, "second_factor")
	results := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			_, err := s.VerifySecondFactor(t.Context(), token, codes[1])
			results <- err
		}()
	}
	start.Done()
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("racing recovery code: %v / %v", first, second)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM recovery_codes`).Scan(&remaining); err != nil || remaining != len(codes)-2 {
		t.Fatalf("remaining recovery codes=%d %v", remaining, err)
	}

	clock.Advance(time.Minute)
	session, err := s.CreateSession(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Test browser")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Minute)
	code := codeAt(t, secret, clock.Now())
	if _, err := s.ReplaceRecoveryCodes(t.Context(), "invalid", fixturePassword, code); !errors.Is(err, ErrCredentials) {
		t.Fatal("recovery codes replaced without an active session")
	}
	if _, err := s.ReplaceRecoveryCodes(t.Context(), session.AccessToken, fixturePassword, codes[2]); !errors.Is(err, ErrCredentials) {
		t.Fatal("a recovery code authorized replacement")
	}
	if _, err := s.ReplaceRecoveryCodes(t.Context(), session.AccessToken, "wrong password", code); !errors.Is(err, ErrCredentials) {
		t.Fatal("wrong password authorized replacement")
	}
	clock.Advance(time.Second)
	replacement, err := s.ReplaceRecoveryCodes(t.Context(), session.AccessToken, fixturePassword, codeAt(t, secret, clock.Now()))
	if err != nil || len(replacement) != twofactor.RecoveryCodeCount {
		t.Fatal(err)
	}
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), codes[2]); !errors.Is(err, ErrCredentials) {
		t.Fatal("replaced recovery code accepted")
	}
	clock.Advance(time.Minute)
	if _, err := s.VerifySecondFactor(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), replacement[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSecondFactorBackoffSurvivesPasswordLoginAndRestart(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	secret, _ := onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	token := login(t, s, "admin", fixturePassword, "second_factor")
	delay := time.Second
	for range database.MaxChallengeFailures {
		if _, err := s.VerifySecondFactor(t.Context(), token, wrongCode(codeAt(t, secret, clock.Now()))); !errors.Is(err, ErrCredentials) {
			t.Fatal(err)
		}
		clock.Advance(delay - time.Millisecond)
		if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); !errors.Is(err, ErrCredentials) {
			t.Fatal("correct code accepted during backoff")
		}
		clock.Advance(time.Millisecond)
		delay *= 2
	}
	if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); !errors.Is(err, ErrCredentials) {
		t.Fatal("challenge survived its failure limit")
	}

	// A correct password neither clears the backoff nor resets its growth.
	token = login(t, s, "admin", fixturePassword, "second_factor")
	if _, err := s.VerifySecondFactor(t.Context(), token, wrongCode(codeAt(t, secret, clock.Now()))); !errors.Is(err, ErrCredentials) {
		t.Fatal(err)
	}
	s = twoFactorService(t, db, clock, 7)
	clock.Advance(delay - time.Millisecond)
	if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); !errors.Is(err, ErrCredentials) {
		t.Fatal("password login or restart reset second-factor backoff")
	}
	clock.Advance(time.Millisecond)
	if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.repo.CredentialsByUsername(t.Context(), "admin"); c.SecondFactorBlockedUntil != 0 {
		t.Fatal("successful second factor left the account blocked")
	}
}

func TestPasswordResetPreservesTOTPAndTOTPResetForcesEnrollment(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	admin := principalFor(t, s, "admin")
	issued, err := s.CreateUser(t.Context(), admin, "user")
	if err != nil {
		t.Fatal(err)
	}
	secret, codes := onboard(t, s, clock, "user", issued.TemporaryPassword)

	if err := s.ResetTOTP(t.Context(), principalFor(t, s, "user"), admin.accountID); !errors.Is(err, database.ErrAdminRequired) {
		t.Fatalf("non-administrator reset TOTP: %v", err)
	}
	stale := admin
	stale.reauthenticatedAt = clock.Now().Add(-6 * time.Minute)
	if err := s.ResetTOTP(t.Context(), stale, issued.AccountID); !errors.Is(err, ErrForbidden) {
		t.Fatal("TOTP reset without administrator reauthentication")
	}

	// Password reset: the same authenticator still completes login afterwards.
	reset, err := s.ResetPassword(t.Context(), admin, issued.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePassword(t.Context(), login(t, s, "user", reset.TemporaryPassword, "replace_password"), "another permanent password"); err != nil {
		t.Fatal(err)
	}
	token := login(t, s, "user", "another permanent password", "second_factor")
	if _, err := s.CreateSession(t.Context(), token, codeAt(t, secret, clock.Now()), "Desktop"); err != nil {
		t.Fatalf("password reset discarded TOTP: %v", err)
	}

	// TOTP reset: the password stays, the old secret and codes are destroyed.
	clock.Advance(time.Minute)
	token = login(t, s, "user", "another permanent password", "second_factor")
	before, _ := s.repo.CredentialsByUsername(t.Context(), "user")
	if err := s.ResetTOTP(t.Context(), admin, issued.AccountID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.repo.CredentialsByUsername(t.Context(), "user")
	if after.Status != (protocol.AccountStatus{State: protocol.TOTPRequired}) || after.Epoch != before.Epoch+1 || after.PasswordHash != before.PasswordHash {
		t.Fatalf("TOTP reset state: %+v", after)
	}
	for _, table := range []string{"totp_secrets", "recovery_codes", "auth_challenges"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE account_id=?`, issued.AccountID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained after TOTP reset: %d %v", table, count, err)
		}
	}
	if _, err := s.VerifySecondFactor(t.Context(), token, codeAt(t, secret, clock.Now())); !errors.Is(err, ErrCredentials) {
		t.Fatal("pre-reset challenge survived")
	}
	token = login(t, s, "user", "another permanent password", "enroll_totp")
	if _, err := s.VerifySecondFactor(t.Context(), token, codes[0]); !errors.Is(err, ErrCredentials) {
		t.Fatal("recovery code survived TOTP reset")
	}
	enrollment, err := s.BeginTOTPEnrollment(t.Context(), token)
	if err != nil || enrollment.Secret == secret {
		t.Fatalf("re-enrollment: %v", err)
	}
	if _, err := s.ConfirmTOTPEnrollment(t.Context(), token, codeAt(t, enrollment.Secret, clock.Now())); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='reset_totp' AND actor_id=? AND target_id=?`, admin.accountID, issued.AccountID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("TOTP reset not audited: %d %v", audits, err)
	}

	// Enrollment alone does not release the recovery lock: full sign-in does.
	if _, err := s.ResetPassword(t.Context(), admin, issued.AccountID); !errors.Is(err, database.ErrRecoveryPending) {
		t.Fatalf("mixed reset accepted before login: %v", err)
	}
	clock.Advance(time.Minute)
	token = login(t, s, "user", "another permanent password", "second_factor")
	if _, err := s.CreateSession(t.Context(), token, codeAt(t, enrollment.Secret, clock.Now()), "Desktop"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword(t.Context(), admin, issued.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetTOTP(t.Context(), admin, issued.AccountID); !errors.Is(err, database.ErrRecoveryPending) {
		t.Fatalf("mixed recovery accepted: %v", err)
	}

}

func TestTwoFactorFailsClosedWithoutUsableProtectionKey(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	secret, _ := onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	token := login(t, s, "admin", fixturePassword, "second_factor")
	code := codeAt(t, secret, clock.Now())

	unconfigured, err := New(database.NewRepository(db), stateTestHasher{}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unconfigured.VerifySecondFactor(t.Context(), token, code); !errors.Is(err, ErrTwoFactorUnavailable) {
		t.Fatalf("unconfigured two-factor: %v", err)
	}
	if _, err := unconfigured.BeginTOTPEnrollment(t.Context(), token); !errors.Is(err, ErrTwoFactorUnavailable) {
		t.Fatal(err)
	}
	if _, err := New(database.NewRepository(db), stateTestHasher{}, clock.Now, WithTwoFactor("invalid", nil)); err == nil {
		t.Fatal("invalid two-factor configuration accepted")
	}
	// A replaced key is a restore fault, not a wrong code: no backoff applies.
	if _, err := twoFactorService(t, db, clock, 8).VerifySecondFactor(t.Context(), token, code); !errors.Is(err, ErrTwoFactorUnavailable) {
		t.Fatalf("wrong protection key: %v", err)
	}
	if has, err := s.repo.HasTOTPSecrets(t.Context()); err != nil || !has {
		t.Fatal("retained secret not reported for key-creation guard")
	}
	if _, err := s.VerifySecondFactor(t.Context(), token, code); err != nil {
		t.Fatalf("configuration faults consumed the challenge or applied backoff: %v", err)
	}
}

package auth

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/testutil"
)

func refreshFixture(t *testing.T) (*Service, *testutil.Clock, api.Session, string) {
	t.Helper()
	s, _, clock := twoFactorFixture(t)
	onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	issued, err := s.CreateUser(t.Context(), principalFor(t, s, "admin"), "user")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := onboard(t, s, clock, "user", issued.TemporaryPassword)
	session, err := s.CreateSession(t.Context(), login(t, s, "user", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Test laptop")
	if err != nil {
		t.Fatal(err)
	}
	return s, clock, session, secret
}
func rotation(session api.Session) api.RefreshRequest {
	return api.RefreshRequest{DeviceID: session.DeviceID, RefreshCredential: session.RefreshCredential, NextRefreshCredential: newSecret(), RotationID: newID()}
}
func rotate(t *testing.T, s *Service, q api.RefreshRequest) api.Session {
	t.Helper()
	result, err := s.Refresh(t.Context(), q)
	if err != nil || result.Session == nil {
		t.Fatalf("refresh failed: %+v %v", result, err)
	}
	out := *result.Session
	out.RefreshCredential = q.NextRefreshCredential
	return out
}
func TestRefreshRotationRetryReplayAndIsolation(t *testing.T) {
	s, clock, session, secret := refreshFixture(t)
	if session.ExpiresAt.Sub(clock.Now()) != 10*time.Minute {
		t.Fatal("desktop access is not short-lived")
	}
	clock.Advance(30 * time.Second)
	second, err := s.CreateSession(t.Context(), login(t, s, "user", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Other device")
	if err != nil {
		t.Fatal(err)
	}
	q := rotation(session)
	first := rotate(t, s, q)
	if _, err = s.Authorize(t.Context(), session.AccessToken); err != nil {
		t.Fatal("rotation broke in-flight access grant", err)
	}
	clock.Advance(time.Hour)
	retry := rotate(t, s, q)
	if !retry.IdleExpiresAt.Equal(first.IdleExpiresAt) || !retry.ReauthenticateAt.Equal(first.ReauthenticateAt) {
		t.Fatal("retry extended deadlines")
	}
	if _, err = s.Authorize(t.Context(), first.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired access accepted")
	}
	q.RotationID = newID()
	q.NextRefreshCredential = newSecret()
	if _, err = s.Refresh(t.Context(), q); !errors.Is(err, ErrCredentials) {
		t.Fatalf("spent credential accepted: %v", err)
	}
	if _, err = s.Authorize(t.Context(), retry.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("reused family not revoked")
	}
	rotate(t, s, rotation(second))
	devices, err := s.repo.Devices(t.Context(), session.AccountID, clock.Now())
	if err != nil || len(devices) != 2 {
		t.Fatal("device history lost", err)
	}
}
func TestRefreshExactInactivityBoundary(t *testing.T) {
	s, clock, session, _ := refreshFixture(t)
	clock.Advance(30 * 24 * time.Hour)
	if _, err := s.Refresh(t.Context(), rotation(session)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("30-day expiry accepted: %v", err)
	}
}
func TestRefreshSixtyDayTOTPDoesNotRequirePassword(t *testing.T) {
	s, clock, session, secret := refreshFixture(t)
	due := session.ReauthenticateAt
	for range 2 {
		clock.Advance(29 * 24 * time.Hour)
		session = rotate(t, s, rotation(session))
		if !session.ReauthenticateAt.Equal(due) {
			t.Fatal("rotation extended TOTP deadline")
		}
	}
	clock.Advance(2 * 24 * time.Hour)
	q := rotation(session)
	result, err := s.Refresh(t.Context(), q)
	if err != nil || result.NextStep != "second_factor" || result.Session != nil {
		t.Fatalf("missing TOTP gate: %+v %v", result, err)
	}
	if _, err = s.CreateSession(t.Context(), result.Challenge, codeAt(t, secret, clock.Now()), "Forged device"); !errors.Is(err, ErrCredentials) {
		t.Fatal("refresh challenge created a new registration")
	}
	q.Challenge = result.Challenge
	q.Code = codeAt(t, secret, clock.Now())
	refreshed := rotate(t, s, q)
	if refreshed.DeviceID != session.DeviceID || !refreshed.ReauthenticateAt.Equal(clock.Now().Add(60*24*time.Hour)) {
		t.Fatal("TOTP renewal changed device or wrong deadline")
	}
	if _, err = s.Authorize(t.Context(), refreshed.AccessToken); err != nil {
		t.Fatal(err)
	}
	// A lost TOTP response can be recovered with the original durable tuple.
	q.Challenge = ""
	q.Code = ""
	rotate(t, s, q)
}
func TestRefreshCannotBypassRevocationAndWrongDeviceProof(t *testing.T) {
	s, clock, session, _ := refreshFixture(t)
	q := rotation(session)
	wrong := q
	wrong.RefreshCredential = newSecret()
	if _, err := s.Refresh(t.Context(), wrong); !errors.Is(err, ErrCredentials) {
		t.Fatal("unknown refresh accepted")
	}
	session = rotate(t, s, q)
	if err := s.repo.RevokeDevice(t.Context(), principalFor(t, s, "admin").accountID, principalFor(t, s, "admin").epoch, session.AccountID, session.DeviceID, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(t.Context(), rotation(session)); !errors.Is(err, ErrCredentials) {
		t.Fatal("revoked device refreshed")
	}
	if _, err := s.Authorize(t.Context(), session.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("revoked access remained valid")
	}
}
func TestConcurrentRecoveryKindsAreMutuallyExclusive(t *testing.T) {
	s, _, session, _ := refreshFixture(t)
	actor := principalFor(t, s, "admin")
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Go(func() { <-start; _, err := s.ResetPassword(t.Context(), actor, session.AccountID); results <- err })
	group.Go(func() { <-start; results <- s.ResetTOTP(t.Context(), actor, session.AccountID) })
	close(start)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, database.ErrRecoveryPending) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("mixed recovery race did not serialize", successes, conflicts)
	}
	if _, err := s.Authorize(t.Context(), session.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("recovery did not revoke access")
	}
}

func TestConcurrentConflictingRotationsRevokeDevice(t *testing.T) {
	s, _, session, _ := refreshFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		q := rotation(session)
		group.Go(func() { <-start; _, err := s.Refresh(t.Context(), q); results <- err })
	}
	close(start)
	group.Wait()
	close(results)
	wins, denied := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrCredentials) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || denied != 1 {
		t.Fatal("conflicting rotations not rejected", wins, denied)
	}
	if _, err := s.Refresh(t.Context(), rotation(session)); !errors.Is(err, ErrCredentials) {
		t.Fatal("compromised device not revoked")
	}
}

func TestExpiredAccessCannotRevokeCurrentRefreshAuthorization(t *testing.T) {
	s, clock, session, _ := refreshFixture(t)
	clock.Advance(11 * time.Minute)
	if err := s.EndSession(t.Context(), session.AccessToken); err != nil {
		t.Fatal(err)
	}
	refreshed := rotate(t, s, rotation(session))
	if err := s.EndSession(t.Context(), refreshed.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(t.Context(), rotation(refreshed)); !errors.Is(err, ErrCredentials) {
		t.Fatal("live logout did not revoke refresh authorization")
	}
}

func TestAccessGrantsCannotCrossDeviceDeadlines(t *testing.T) {
	t.Run("idle expiry on exact retry", func(t *testing.T) {
		s, clock, session, _ := refreshFixture(t)
		q := rotation(session)
		first := rotate(t, s, q)
		clock.Advance(30*24*time.Hour - 30*time.Second)
		retry := rotate(t, s, q)
		if !retry.ExpiresAt.Equal(first.IdleExpiresAt) {
			t.Fatal("retried grant crossed inactivity deadline")
		}
		clock.Advance(30 * time.Second)
		if _, err := s.Authorize(t.Context(), retry.AccessToken); !errors.Is(err, ErrCredentials) {
			t.Fatal("idle-expired access still valid")
		}
	})
	t.Run("second-factor deadline", func(t *testing.T) {
		s, clock, session, _ := refreshFixture(t)
		due := session.ReauthenticateAt
		for range 2 {
			clock.Advance(29 * 24 * time.Hour)
			session = rotate(t, s, rotation(session))
		}
		clock.Advance(2*24*time.Hour - 30*time.Second)
		session = rotate(t, s, rotation(session))
		if !session.ExpiresAt.Equal(due) {
			t.Fatal("access grant crossed TOTP deadline")
		}
		clock.Advance(30 * time.Second)
		if _, err := s.Authorize(t.Context(), session.AccessToken); !errors.Is(err, ErrCredentials) {
			t.Fatal("TOTP-expired access still valid")
		}
	})
}

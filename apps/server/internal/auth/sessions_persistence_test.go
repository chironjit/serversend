package auth

import (
	"errors"
	"testing"
	"time"
)

func TestRememberedSessionExpiryRevocationAndFreshAdmin(t *testing.T) {
	s, _, clock := twoFactorFixture(t)
	issued := bootstrapFixture(t, s)
	secret, _ := onboard(t, s, clock, "admin", issued.TemporaryPassword)
	challenge := login(t, s, "admin", fixturePassword, "second_factor")
	session, err := s.CreateSession(t.Context(), challenge, codeAt(t, secret, clock.Now()), "Browser")
	if err != nil {
		t.Fatal(err)
	}
	if session.ExpiresAt.Sub(clock.Now()) != 7*24*time.Hour {
		t.Fatal("session must last seven days")
	}
	clock.Advance(6 * 24 * time.Hour)
	if _, err = s.Authorize(t.Context(), session.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateUserFromSession(t.Context(), session.AccessToken, "newuser"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("old admin session created user: %v", err)
	}
	clock.Advance(24 * time.Hour)
	if _, err = s.Authorize(t.Context(), session.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expired session allowed: %v", err)
	}
}

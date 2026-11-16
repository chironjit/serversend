package auth

import (
	"bytes"
	"github.com/chironjit/serversend/shared/protocol"
	"testing"
)

func TestPendingPasswordsPersistAndDisappearAfterReplacement(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	actor := principalFor(t, s, "admin")
	first, err := s.CreateUser(t.Context(), actor, "alice")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(1e9)
	second, err := s.CreateUser(t.Context(), actor, "bob")
	if err != nil {
		t.Fatal(err)
	}
	restored := twoFactorService(t, db, clock, 7)
	users, err := restored.repo.Users(t.Context(), clock.Now())
	if err != nil || len(users) != 2 {
		t.Fatalf("users: %+v %v", users, err)
	}
	for _, u := range users {
		expected := first.TemporaryPassword
		if u.ID == second.AccountID {
			expected = second.TemporaryPassword
		}
		if bytes.Contains(u.Protected, []byte(expected)) {
			t.Fatal("plaintext stored")
		}
		actual, err := restored.protector.OpenPassword(protocol.Scope{ServerID: restored.serverID, AccountID: u.ID}, u.Protected)
		if err != nil || actual != expected {
			t.Fatalf("password after service restart: %v", err)
		}
	}
	clock.Advance(1e9)
	ch := login(t, restored, "alice", first.TemporaryPassword, "replace_password")
	if err := restored.ReplacePassword(t.Context(), ch, "a brand new permanent password"); err != nil {
		t.Fatal(err)
	}
	users, err = restored.repo.Users(t.Context(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == first.AccountID && len(u.Protected) != 0 {
			t.Fatal("replaced password retained")
		}
	}
	clock.Advance(TemporaryPasswordLifetime)
	users, err = restored.repo.Users(t.Context(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if len(u.Protected) != 0 {
			t.Fatal("expired password retained")
		}
	}
	if _, err := restored.Users(t.Context(), "invalid"); err == nil {
		t.Fatal("unauthenticated list accepted")
	}
}

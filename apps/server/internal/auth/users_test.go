package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTemporaryPasswordIsOnlyDeliveredOnIssuance(t *testing.T) {
	s, db, clock := twoFactorFixture(t)
	secret, _ := onboard(t, s, clock, "admin", bootstrapFixture(t, s).TemporaryPassword)
	clock.Advance(time.Minute)
	admin, err := s.CreateSession(t.Context(), login(t, s, "admin", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Test browser")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.CreateUserFromSession(t.Context(), admin.AccessToken, "alice")
	if err != nil {
		t.Fatal(err)
	}
	restored := twoFactorService(t, db, clock, 7)
	users, err := restored.Users(t.Context(), admin.AccessToken)
	if err != nil || len(users) != 1 || users[0].ExpiresAt == nil {
		t.Fatalf("users: %+v %v", users, err)
	}
	data, err := json.Marshal(users)
	if err != nil || strings.Contains(string(data), issued.TemporaryPassword) || strings.Contains(string(data), "temporary_password") {
		t.Fatal("account listing exposed the issued credential")
	}
	ch := login(t, restored, "alice", issued.TemporaryPassword, "replace_password")
	if err := restored.ReplacePassword(t.Context(), ch, "a brand new permanent password"); err != nil {
		t.Fatal(err)
	}
	users, err = restored.Users(t.Context(), admin.AccessToken)
	if err != nil || len(users) != 1 || users[0].ExpiresAt != nil {
		t.Fatalf("users after replacement: %+v %v", users, err)
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/pquerna/otp/totp"
)

func postJSON(t *testing.T, client *http.Client, url, token string, request, response any) int {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Body.Close()
	data, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response != nil && w.StatusCode == 200 {
		if err := json.Unmarshal(data, response); err != nil {
			t.Fatal(err)
		}
	}
	return w.StatusCode
}

// The process flow uses production Argon2id, the durable protection key and
// real TLS: operator bootstrap, mandatory onboarding, then restart behaviour.
func TestProcessOnboardingAndProtectionKeyRestoreGuard(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = s.Close()
		}
	})
	keyPath := filepath.Join(c.ConfigDir, "totp.key")
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("protection key not created privately: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	client := testClient(t, c)
	base := "https://" + listener.Addr().String() + api.BasePath

	// No network setup path exists before the local operator bootstraps.
	if status := postJSON(t, client, base+"/auth/password", "", api.PasswordAuthentication{Username: "admin", Password: "anything at all"}, nil); status != 401 {
		t.Fatalf("login before bootstrap: %d", status)
	}
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT password_hash FROM accounts`).Scan(&stored); err != nil || !strings.HasPrefix(stored, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("temporary password is not stored as production Argon2id: %v", err)
	}
	var attempt api.PasswordAttempt
	if status := postJSON(t, client, base+"/auth/password", "", api.PasswordAuthentication{Username: "admin", Password: issued.TemporaryPassword}, &attempt); status != 200 || attempt.NextStep != "replace_password" {
		t.Fatalf("temporary login: %d %q", status, attempt.NextStep)
	}
	if status := postJSON(t, client, base+"/auth/password/replace", attempt.Challenge, api.PasswordReplacement{Password: "a long permanent password"}, nil); status != 204 {
		t.Fatalf("replacement: %d", status)
	}
	if status := postJSON(t, client, base+"/auth/password", "", api.PasswordAuthentication{Username: "admin", Password: "a long permanent password"}, &attempt); status != 200 || attempt.NextStep != "enroll_totp" {
		t.Fatalf("permanent login: %d %q", status, attempt.NextStep)
	}
	var enrollment api.TOTPEnrollment
	if status := postJSON(t, client, base+"/auth/totp/enroll", attempt.Challenge, struct{}{}, &enrollment); status != 200 {
		t.Fatalf("enrollment: %d", status)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var codes api.RecoveryCodes
	if status := postJSON(t, client, base+"/auth/totp/confirm", attempt.Challenge, api.SecondFactorCode{Code: code}, &codes); status != 200 || len(codes.Codes) != 10 {
		t.Fatalf("confirmation: %d", status)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	// Restart with the key: the stored secret still authenticates.
	restarted, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	login, err := restarted.Accounts().AuthenticatePassword(t.Context(), "admin", "a long permanent password")
	if err != nil || login.NextStep != "second_factor" {
		t.Fatalf("login after restart: %q %v", login.NextStep, err)
	}
	if _, err := restarted.Accounts().VerifySecondFactor(t.Context(), login.Challenge, codes.Codes[0]); err != nil {
		t.Fatalf("recovery code after restart: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}

	// A lost key beside retained secrets stops startup; it is never replaced.
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), c); err == nil || !strings.Contains(err.Error(), "restore totp.key") {
		t.Fatalf("startup generated a replacement protection key: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("replacement protection key was written")
	}
}

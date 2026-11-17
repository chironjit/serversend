package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
)

func TestDurableRefreshRetriesLostResponseAcrossRestart(t *testing.T) {
	initial := api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", DeviceID: "33333333333333333333333333333333", Username: "alice", Role: "user", RefreshCredential: "old-refresh"}
	var mu sync.Mutex
	var persisted CredentialState
	var first *api.RefreshRequest
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		var q api.RefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		if persisted.Pending == nil || persisted.Pending.RotationID != q.RotationID || persisted.Pending.NextRefreshCredential != q.NextRefreshCredential {
			t.Error("rotation sent before durable save")
		}
		if first == nil {
			copy := q
			first = &copy
			w.WriteHeader(503)
			return
		} // Simulate a committed request with a lost/unreadable response.
		if q != *first {
			t.Error("restart changed pending rotation")
		}
		out := initial
		out.RefreshCredential = ""
		out.AccessToken = "new-access"
		out.ExpiresAt = time.Now().Add(10 * time.Minute)
		out.ReauthenticateAt = time.Now().Add(60 * 24 * time.Hour)
		_ = json.NewEncoder(w).Encode(api.RefreshResult{Session: &out})
	}))
	defer server.Close()
	store := func(state CredentialState) error { mu.Lock(); defer mu.Unlock(); persisted = state; return nil }
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCredentialStore(store)
	if err = c.RestoreCredentials(t.Context(), CredentialState{Session: initial}); err == nil {
		t.Fatal("lost response ignored")
	}
	c.Close()
	mu.Lock()
	saved := persisted
	mu.Unlock()
	restarted, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.SetCredentialStore(store)
	if err = restarted.RestoreCredentials(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if persisted.Pending != nil || persisted.Session.RefreshCredential != first.NextRefreshCredential || persisted.Session.AccessToken != "" {
		t.Fatal("successor not safely promoted")
	}
	public, _ := restarted.Account()
	if public.AccessToken != "" || public.RefreshCredential != "" {
		t.Fatal("credential in public account state")
	}
	if calls != 2 {
		t.Fatal("unexpected refresh count")
	}
}
func TestRefreshDoesNotSendWhenCredentialStoreIsLocked(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetCredentialStore(func(CredentialState) error { return errors.New("keychain locked") })
	err = c.RestoreCredentials(context.Background(), CredentialState{Session: api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", DeviceID: "33333333333333333333333333333333", RefreshCredential: "secret"}})
	if err == nil || requests != 0 {
		t.Fatal("sent rotation despite failing to save successor")
	}
}
func TestConcurrentWorkersShareOneRotation(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	session := api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", DeviceID: "33333333333333333333333333333333", RefreshCredential: "secret", AccessToken: "expired"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		out := session
		out.RefreshCredential = ""
		out.AccessToken = "fresh"
		out.ExpiresAt = time.Now().Add(10 * time.Minute)
		out.ReauthenticateAt = time.Now().Add(60 * 24 * time.Hour)
		_ = json.NewEncoder(w).Encode(api.RefreshResult{Session: &out})
	}))
	defer server.Close()
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.session = session
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			if err := c.EnsureSession(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatal("workers rotated concurrently", calls)
	}
}

func TestFailedSuccessorPromotionKeepsRecoverableTuple(t *testing.T) {
	session := api.Session{Role: "user", ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", DeviceID: "33333333333333333333333333333333", RefreshCredential: "old"}
	var first api.RefreshRequest
	var mu sync.Mutex
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var q api.RefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		requests++
		if requests == 1 {
			first = q
		} else if q != first {
			t.Error("promotion failure changed the rotation tuple")
		}
		out := session
		out.RefreshCredential = ""
		out.AccessToken = "access"
		out.ExpiresAt = time.Now().Add(10 * time.Minute)
		out.ReauthenticateAt = time.Now().Add(60 * 24 * time.Hour)
		_ = json.NewEncoder(w).Encode(api.RefreshResult{Session: &out})
	}))
	defer server.Close()
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saves := 0
	var durable CredentialState
	c.SetCredentialStore(func(state CredentialState) error {
		saves++
		if saves == 2 {
			return errors.New("keychain became locked")
		}
		durable = state
		return nil
	})
	if err = c.RestoreCredentials(t.Context(), CredentialState{Session: session}); err == nil {
		t.Fatal("failed credential promotion ignored")
	}
	if err = c.EnsureSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	if durable.Pending != nil || durable.Session.RefreshCredential != first.NextRefreshCredential {
		t.Fatal("could not finish interrupted promotion")
	}
}

func TestTOTPChallengeIsSharedByWorkersUntilVerification(t *testing.T) {
	session := api.Session{Role: "user", ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", DeviceID: "33333333333333333333333333333333", RefreshCredential: "secret"}
	var mu sync.Mutex
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		var q api.RefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		if q.Code == "" {
			_ = json.NewEncoder(w).Encode(api.RefreshResult{NextStep: "second_factor", Challenge: "restricted-challenge"})
			return
		}
		if q.Challenge != "restricted-challenge" || q.Code != "123456" {
			t.Error("TOTP not bound to device challenge")
		}
		out := session
		out.RefreshCredential = ""
		out.AccessToken = "access"
		out.ExpiresAt = time.Now().Add(10 * time.Minute)
		out.ReauthenticateAt = time.Now().Add(60 * 24 * time.Hour)
		_ = json.NewEncoder(w).Encode(api.RefreshResult{Session: &out})
	}))
	defer server.Close()
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetCredentialStore(func(state CredentialState) error {
		if state.Pending != nil && (state.Pending.Code != "" || state.Pending.Challenge != "") {
			t.Error("TOTP persisted in credential store")
		}
		return nil
	})
	if err = c.RestoreCredentials(t.Context(), CredentialState{Session: session}); !errors.Is(err, ErrTOTPRequired) {
		t.Fatal(err)
	}
	for range 5 {
		if err = c.EnsureSession(t.Context()); !errors.Is(err, ErrTOTPRequired) {
			t.Fatal(err)
		}
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 1 {
		t.Fatal("workers churned TOTP challenges", count)
	}
	if err = c.Reauthenticate(t.Context(), "123456"); err != nil {
		t.Fatal(err)
	}
	if err = c.EnsureSession(t.Context()); err != nil {
		t.Fatal(err)
	}
}

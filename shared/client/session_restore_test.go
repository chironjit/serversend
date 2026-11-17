package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chironjit/serversend/shared/api"
)

func TestRestoreSessionChecksLiveAccountAndServer(t *testing.T) {
	original := api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "secret-canary", Role: "user"}
	current := original
	status := 200
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/current" || r.Header.Get("Authorization") != "Bearer secret-canary" {
			t.Error("restoration did not authenticate")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(current)
	}))
	defer server.Close()
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.RestoreSession(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if c.SavedSession().AccessToken != original.AccessToken {
		t.Fatal("session not restored")
	}
	public, _ := c.Account()
	if public.AccessToken != "" {
		t.Fatal("credential exposed in account state")
	}
	for _, tc := range []string{"account", "server", "revoked", "role"} {
		t.Run(tc, func(t *testing.T) {
			current = original
			status = 200
			switch tc {
			case "account":
				current.AccountID = "33333333333333333333333333333333"
			case "server":
				current.ServerID = "33333333333333333333333333333333"
			case "role":
				current.Role = "admin"
			case "revoked":
				status = 401
			}
			if err = c.RestoreSession(t.Context(), original); err == nil {
				t.Fatal("invalid session accepted")
			}
		})
	}
}

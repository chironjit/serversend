package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/zalando/go-keyring"
)

func profileTrust() client.Trust {
	return client.Trust{Identity: protocol.TrustedIdentity{ServerID: "11111111111111111111111111111111", PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}}
}
func profileState() client.CredentialState {
	return client.CredentialState{Session: api.Session{DeviceID: "33333333333333333333333333333333", ServerID: profileTrust().Identity.ServerID, AccountID: "22222222222222222222222222222222", RefreshCredential: "refresh-canary", AccessToken: "access-canary", Username: "user"}}
}
func TestProfilesSurviveRestartAndCredentialsStayInKeychain(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "servers.json")
	m := &Manager{profilePath: path}
	for _, origin := range []string{"https://127.0.0.1:58443", "https://127.0.0.2:58443"} {
		if err := m.saveTrustLocked(client.ServerProbe{Origin: origin, Fingerprint: strings.Repeat("a", 64)}, profileTrust()); err != nil {
			t.Fatal(err)
		}
	}
	m.profiles[0].Remember = true
	m.profiles[0].AccountID = profileState().Session.AccountID
	if err := storeCredentials(m.profiles[0].Origin, profileTrust(), profileState()); err != nil {
		t.Fatal(err)
	}
	if err := m.writeProfilesLocked(); err != nil {
		t.Fatal(err)
	}
	raw, err := keyring.Get("ServerSend", profileCredentialID(m.profiles[0]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "access-canary") {
		t.Fatal("access grant persisted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "canary") {
		t.Fatal("credential leaked to profile")
	}
	var restored []Profile
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || !restored[0].Remember || restored[0].InstallationID == "" || restored[0].InstallationID != m.profiles[0].InstallationID {
		t.Fatal("profile/installation lost")
	}
	changed := profileTrust()
	changed.Identity.ServerID = "33333333333333333333333333333333"
	if err = m.saveTrustLocked(client.ServerProbe{Origin: m.profiles[0].Origin}, changed); err == nil {
		t.Fatal("identity silently replaced")
	}
	if err = m.RemoveProfile(m.profiles[0].Origin); err != nil {
		t.Fatal(err)
	}
	if _, err = keyring.Get("ServerSend", profileCredentialID(restored[0])); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("credential retained", err)
	}
}
func TestCredentialScopeLockedUnavailableAndLostStore(t *testing.T) {
	trust, state := profileTrust(), profileState()
	origin := "https://127.0.0.1:58443"
	keyring.MockInit()
	if err := storeCredentials(origin, trust, state); err != nil {
		t.Fatal(err)
	}
	first := Profile{Origin: origin, Trust: trust, AccountID: state.Session.AccountID}
	for _, scope := range []string{"account", "server", "origin"} {
		p := first
		switch scope {
		case "account":
			p.AccountID = "33333333333333333333333333333333"
		case "server":
			p.Trust.Identity.ServerID = "33333333333333333333333333333333"
		case "origin":
			p.Origin = "https://127.0.0.2:58443"
		}
		if _, err := keyring.Get("ServerSend", profileCredentialID(p)); !errors.Is(err, keyring.ErrNotFound) {
			t.Fatal("credentials crossed", scope)
		}
	}
	state.Session.ServerID = "33333333333333333333333333333333"
	if err := storeCredentials(origin, trust, state); err == nil {
		t.Fatal("mismatched trust saved")
	}
	for _, failure := range []string{"locked", "unavailable"} {
		t.Run(failure, func(t *testing.T) {
			keyring.MockInitWithError(errors.New(failure))
			path := filepath.Join(t.TempDir(), "servers.json")
			first.Remember = true
			m := &Manager{profilePath: path, profiles: []Profile{first}}
			if err := m.writeProfilesLocked(); err != nil {
				t.Fatal(err)
			}
			if _, err := m.OpenProfile(t.Context(), origin); err == nil {
				t.Fatal("failed store silently ignored")
			}
			defer m.client.Close()
			if err := storeCredentials(origin, trust, profileState()); err == nil {
				t.Fatal("failed secure save ignored")
			}
			if err := m.forgetLogin(origin); err == nil {
				t.Fatal("keychain deletion failure hidden")
			}
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), "canary") || m.profiles[0].Remember {
				t.Fatal("plaintext fallback or remembered offline logout")
			}
		})
	}
	keyring.MockInit() // Models an item removed outside the application.
	m := &Manager{profiles: []Profile{first}}
	account, err := m.OpenProfile(t.Context(), origin)
	if err != nil || account.Username != "" {
		t.Fatal("lost store should require full login", err)
	}
	// Losing the item must not disable persistence for the next full login.
	// Cancel networking to prove the configured store is consulted first.
	keyring.MockInitWithError(errors.New("locked after loss"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = m.client.RestoreCredentials(ctx, profileState()); err == nil || !strings.Contains(err.Error(), "credential store") {
		t.Fatal("lost item disabled future rotation persistence", err)
	}
	m.client.Close()
}

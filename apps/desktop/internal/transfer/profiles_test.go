package transfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/client"
	"github.com/zalando/go-keyring"
)

func TestProfilesSurviveRestartAndCredentialsStayInKeychain(t *testing.T) {
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "servers.json")
	m := &Manager{profilePath: path}
	for _, origin := range []string{"https://127.0.0.1:58443", "https://127.0.0.2:58443"} {
		if err := m.saveTrustLocked(client.ServerProbe{Origin: origin, Fingerprint: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	m.profiles[0].Remember = true
	c, err := client.New(m.profiles[0].Origin, m.profiles[0].Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = m.saveLogin(c); err != nil {
		t.Fatal(err)
	}
	raw, err := keyring.Get("ServerSend", credentialID(c.Origin()))
	if err != nil {
		t.Fatal(err)
	}
	var session client.CredentialState
	if err = json.Unmarshal([]byte(raw), &session); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "access_token") {
		t.Fatal("credential leaked into profile file")
	}
	var restored []Profile
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || !restored[0].Remember || restored[1].Origin != m.profiles[1].Origin {
		t.Fatal("profiles lost")
	}
	if err = m.saveTrustLocked(client.ServerProbe{Origin: c.Origin(), Fingerprint: strings.Repeat("b", 64)}); err == nil {
		t.Fatal("trust pin replaced")
	}
	if err = m.RemoveProfile(c.Origin()); err != nil {
		t.Fatal(err)
	}
	if _, err = keyring.Get("ServerSend", credentialID(c.Origin())); err != keyring.ErrNotFound {
		t.Fatalf("credential retained: %v", err)
	}
}

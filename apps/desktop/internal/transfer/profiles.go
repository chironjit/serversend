package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/zalando/go-keyring"
)

// Profile contains public connection preferences; credentials live in the OS keychain.
type Profile struct {
	Origin      string `json:"origin"`
	Fingerprint string `json:"fingerprint"`
	Username    string `json:"username"`
	Remember    bool   `json:"remember"`
}

func credentialID(origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) LoadProfiles() ([]Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profilePath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "ServerSend", "servers.json")
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &m.profiles); err != nil {
				return nil, err
			}
		}
		m.profilePath = path
	}
	return append([]Profile{}, m.profiles...), nil
}
func (m *Manager) writeProfilesLocked() error {
	if m.profilePath == "" {
		return nil
	} // Test managers have no persistent store.
	if err := os.MkdirAll(filepath.Dir(m.profilePath), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.profiles, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.profilePath), ".servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), m.profilePath)
}
func (m *Manager) saveTrustLocked(probe client.ServerProbe) error {
	for _, p := range m.profiles {
		if p.Origin == probe.Origin {
			if p.Fingerprint != probe.Fingerprint {
				return errors.New("remove the saved server before changing its certificate trust")
			}
			return nil
		}
	}
	m.profiles = append(m.profiles, Profile{Origin: probe.Origin, Fingerprint: probe.Fingerprint})
	return m.writeProfilesLocked()
}
func (m *Manager) SetRemember(remember bool) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return errors.New("connect first")
	}
	for i := range m.profiles {
		if m.profiles[i].Origin == m.client.Origin() {
			if !remember && m.profiles[i].Remember {
				if err := deleteCredential(m.client.Origin()); err != nil {
					return err
				}
			}
			m.profiles[i].Remember = remember
		}
	}
	m.configureStoreLocked(m.client)
	return m.writeProfilesLocked()
}
func deleteCredential(origin string) error {
	err := keyring.Delete("ServerSend", credentialID(origin))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
func (m *Manager) saveLogin(c *client.Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.profiles {
		p := &m.profiles[i]
		if p.Origin != c.Origin() {
			continue
		}
		session := c.SavedSession()
		p.Username = session.Username
		if p.Remember {
			data, err := json.Marshal(c.Credentials())
			if err != nil {
				return err
			}
			if err = keyring.Set("ServerSend", credentialID(p.Origin), string(data)); err != nil {
				return errors.New("could not save login in OS credential store; unlock your keychain or turn off Remember sign-in")
			}
		}
	}
	return m.writeProfilesLocked()
}
func (m *Manager) forgetLogin(origin string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.profiles {
		if p.Origin == origin && p.Remember {
			return deleteCredential(origin)
		}
	}
	return nil
}
func (m *Manager) OpenProfile(ctx context.Context, origin string) (Account, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if err := m.idle(); err != nil {
		return Account{}, err
	}
	m.mu.Lock()
	var profile Profile
	for _, p := range m.profiles {
		if p.Origin == origin {
			profile = p
		}
	}
	m.mu.Unlock()
	if profile.Origin == "" {
		return Account{}, errors.New("saved server not found")
	}
	c, err := client.New(profile.Origin, profile.Fingerprint)
	if err != nil {
		return Account{}, err
	}
	m.mu.Lock()
	if m.client != nil {
		m.client.Close()
	}
	m.client = c
	c.SetAppVersion(m.version)
	m.password = ""
	m.challenge = ""
	m.catalog = map[string]api.CatalogEntry{}
	m.mu.Unlock()
	if !profile.Remember {
		return Account{}, nil
	}
	raw, err := keyring.Get("ServerSend", credentialID(origin))
	if errors.Is(err, keyring.ErrNotFound) {
		return Account{}, nil
	}
	if err != nil {
		return Account{}, errors.New("unlock your OS credential store to restore sign-in")
	}
	var state client.CredentialState
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return Account{}, errors.New("saved login is invalid; sign in again")
	}
	c.SetCredentialStore(func(state client.CredentialState) error { return storeCredentials(origin, state) })
	err = c.RestoreCredentials(ctx, state)
	if err != nil && !errors.Is(err, client.ErrTOTPRequired) {
		return Account{}, message(err)
	}
	return Account{Username: state.Session.Username, Origin: origin, ServerID: string(state.Session.ServerID), ReauthenticationRequired: errors.Is(err, client.ErrTOTPRequired)}, nil
}
func (m *Manager) RemoveProfile(origin string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if err := m.idle(); err != nil {
		return err
	}
	if err := m.forgetLogin(origin); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil && m.client.Origin() == origin {
		m.client.Close()
		m.client = nil
		m.catalog = map[string]api.CatalogEntry{}
	}
	for i, p := range m.profiles {
		if p.Origin == origin {
			m.profiles = append(m.profiles[:i], m.profiles[i+1:]...)
			break
		}
	}
	return m.writeProfilesLocked()
}

func storeCredentials(origin string, state client.CredentialState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = keyring.Set("ServerSend", credentialID(origin), string(data)); err != nil {
		return errors.New("unlock your OS credential store to save the device session")
	}
	return nil
}
func (m *Manager) configureStoreLocked(c *client.Client) {
	for _, p := range m.profiles {
		if p.Origin == c.Origin() {
			if p.Remember {
				origin := p.Origin
				c.SetCredentialStore(func(state client.CredentialState) error { return storeCredentials(origin, state) })
			} else {
				c.SetCredentialStore(nil)
			}
			return
		}
	}
}
func (m *Manager) SessionStatus(ctx context.Context) (Step, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return Step{Next: "connect"}, nil
	}
	if err = c.Heartbeat(ctx); errors.Is(err, client.ErrTOTPRequired) {
		return Step{Next: "reauth"}, nil
	}
	var typed api.Error
	if errors.As(err, &typed) && typed.Code == api.AuthenticationRequired {
		return Step{Next: "password"}, nil
	}
	if err != nil {
		return Step{}, message(err)
	}
	return Step{Next: "files"}, nil
}
func (m *Manager) CompleteReauthentication(ctx context.Context, code string) (Account, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return Account{}, err
	}
	if err = c.Reauthenticate(ctx, strings.TrimSpace(code)); err != nil {
		return Account{}, message(err)
	}
	session, _ := c.Account()
	return Account{Username: session.Username, Origin: c.Origin(), ServerID: string(session.ServerID)}, nil
}

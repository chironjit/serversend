package transfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/zalando/go-keyring"
)

// Profile contains public connection preferences; credentials live in the OS keychain.
type Profile struct {
	Trust          client.Trust `json:"trust"`
	AccountID      protocol.ID  `json:"account_id,omitempty"`
	InstallationID protocol.ID  `json:"installation_id,omitempty"`
	Origin         string       `json:"origin"`
	Fingerprint    string       `json:"fingerprint"`
	Username       string       `json:"username"`
	Remember       bool         `json:"remember"`
}

func credentialID(scope ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(scope, "\x00")))
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
func (m *Manager) saveTrustLocked(probe client.ServerProbe, trust client.Trust) error {
	if trust.Identity.Validate() != nil {
		return errors.New("verify the server identity before saving trust")
	}
	for i, p := range m.profiles {
		if p.Origin != probe.Origin {
			continue
		}
		if p.Trust.Identity.ServerID != "" && p.Trust.Identity != trust.Identity {
			return errors.New("saved server identity changed; remove the profile and pair explicitly")
		}
		// Legacy certificate-only profiles require explicit repair and a fresh login.
		if p.Trust.Identity.ServerID == "" {
			if err := deleteCredential(p.Origin); err != nil {
				return err
			}
			m.profiles[i].AccountID = ""
		}
		if m.profiles[i].InstallationID == "" {
			id := make([]byte, 16)
			if _, err := rand.Read(id); err != nil {
				return err
			}
			m.profiles[i].InstallationID = protocol.ID(hex.EncodeToString(id))
		}
		m.profiles[i].Trust = trust
		m.profiles[i].Fingerprint = probe.Fingerprint
		return m.writeProfilesLocked()
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	m.profiles = append(m.profiles, Profile{Origin: probe.Origin, Fingerprint: probe.Fingerprint, Trust: trust, InstallationID: protocol.ID(hex.EncodeToString(id))})
	return m.writeProfilesLocked()
}
func profileCredentialID(p Profile) string {
	return credentialID("device-v2", p.Origin, string(p.Trust.Identity.ServerID), p.Trust.Identity.PublicKey, string(p.AccountID))
}
func deleteProfileCredential(p Profile) error {
	if p.AccountID == "" {
		return nil
	}
	return deleteCredentialKey(profileCredentialID(p))
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
				if err := deleteProfileCredential(m.profiles[i]); err != nil {
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
	return deleteCredentialKey(credentialID(origin))
}
func deleteCredentialKey(key string) error {
	err := keyring.Delete("ServerSend", key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
func (m *Manager) saveLogin(c *client.Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	trust, err := c.VerifiedTrust()
	if err != nil {
		return err
	}
	state := c.Credentials()
	if state.Session.ServerID != trust.Identity.ServerID || state.Session.AccountID.Validate() != nil {
		return errors.New("server/account scope mismatch")
	}
	for i := range m.profiles {
		p := &m.profiles[i]
		if p.Origin != c.Origin() {
			continue
		}
		if p.Trust.Identity != trust.Identity {
			return errors.New("server trust changed")
		}
		if p.AccountID != "" && p.AccountID != state.Session.AccountID {
			if err = deleteProfileCredential(*p); err != nil {
				return err
			}
		}
		p.Username = state.Session.Username
		p.AccountID = state.Session.AccountID
		if p.Remember {
			if err = storeCredentials(p.Origin, trust, state); err != nil {
				return err
			}
		}
	}
	return m.writeProfilesLocked()
}
func (m *Manager) forgetLogin(origin string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.profiles {
		if p.Origin == origin {
			// Persist local logout before touching a possibly locked/unavailable keychain.
			m.profiles[i].Remember = false
			if err := m.writeProfilesLocked(); err != nil {
				return err
			}
			if m.client != nil && m.client.Origin() == origin {
				m.client.SetCredentialStore(nil)
			}
			return deleteProfileCredential(p)
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
	if profile.Trust.Identity.Validate() != nil {
		return Account{}, errors.New("this saved server needs trust repair; check its address and import its CA certificate again")
	}
	c, err := client.NewVerified(profile.Origin, profile.Trust)
	if err != nil {
		return Account{}, err
	}
	m.mu.Lock()
	if m.client != nil {
		m.client.Close()
	}
	m.client = c
	c.SetAppVersion(m.version)
	c.SetInstallationID(profile.InstallationID)
	m.configureStoreLocked(c)
	m.password = ""
	m.challenge = ""
	m.catalog = map[string]api.CatalogEntry{}
	m.mu.Unlock()
	if !profile.Remember {
		return Account{}, nil
	}
	raw, err := keyring.Get("ServerSend", profileCredentialID(profile))
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
	if state.Session.AccountID != profile.AccountID || state.Session.ServerID != profile.Trust.Identity.ServerID {
		return Account{}, errors.New("saved credentials belong to a different server or account")
	}
	c.SetCredentialStore(func(state client.CredentialState) error { return storeCredentials(origin, profile.Trust, state) })
	err = c.RestoreCredentials(ctx, state)
	if err != nil && !errors.Is(err, client.ErrTOTPRequired) {
		return Account{}, message(err)
	}
	current, _ := c.Account()
	m.wakeAuthenticated(c)
	return Account{Scope: string(current.ServerID) + "/" + string(current.AccountID), Username: current.Username, Origin: origin, ServerID: string(current.ServerID), ReauthenticationRequired: errors.Is(err, client.ErrTOTPRequired)}, nil
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

func storeCredentials(origin string, trust client.Trust, state client.CredentialState) error {
	if trust.Identity.Validate() != nil || state.Session.ServerID != trust.Identity.ServerID || state.Session.AccountID.Validate() != nil || state.Session.RefreshCredential == "" {
		return errors.New("cannot save unverified or incorrectly scoped credentials")
	}
	state.Session.AccessToken = ""
	state.Session.CSRFToken = ""
	if state.Pending != nil {
		copy := *state.Pending
		copy.Challenge = ""
		copy.Code = ""
		state.Pending = &copy
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	key := profileCredentialID(Profile{Origin: origin, Trust: trust, AccountID: state.Session.AccountID})
	if err = keyring.Set("ServerSend", key, string(data)); err != nil {
		return errors.New("unlock your OS credential store to save the device session, or turn off Remember sign-in")
	}
	return nil
}
func (m *Manager) configureStoreLocked(c *client.Client) {
	for _, p := range m.profiles {
		if p.Origin == c.Origin() {
			c.SetInstallationID(p.InstallationID)
			if p.Remember {
				origin, trust := p.Origin, p.Trust
				c.SetCredentialStore(func(state client.CredentialState) error { return storeCredentials(origin, trust, state) })
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
	m.wakeAuthenticated(c)
	return Account{Scope: string(session.ServerID) + "/" + string(session.AccountID), Username: session.Username, Origin: c.Origin(), ServerID: string(session.ServerID)}, nil
}

// ProbeAuthority accepts only a public CA, supplied locally by the operator.
func (m *Manager) ProbeAuthority(ctx context.Context, origin, authority string) (client.ServerProbe, error) {
	fingerprint, err := client.AuthorityFingerprint(authority)
	if err != nil {
		return client.ServerProbe{}, err
	}
	probe, err := m.Probe(ctx, origin)
	if err != nil {
		return probe, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authority = authority
	probe.AuthorityFingerprint = fingerprint
	m.probe = probe
	return probe, nil
}

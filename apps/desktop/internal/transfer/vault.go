package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"github.com/zalando/go-keyring"
	"time"
)

func (m *Manager) privateKey(scope protocol.Scope) (vault.Key, error) {
	c, err := m.current()
	if err != nil {
		return vault.Key{}, vault.ErrLocked
	}
	a, ok := c.Account()
	if !ok || scope != (protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}) {
		return vault.Key{}, vault.ErrLocked
	}
	return c.VaultKey()
}
func (m *Manager) SetupVault(ctx context.Context, pass string, acknowledged bool) (string, error) {
	if !acknowledged {
		return "", errors.New("acknowledge that lost vault keys cannot be recovered by an administrator")
	}
	c, err := m.current()
	if err != nil {
		return "", err
	}
	a, ok := c.Account()
	if !ok {
		return "", vault.ErrLocked
	}
	e, r, k, err := vault.Create(protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}, pass)
	if err != nil {
		return "", err
	}
	defer clear(k[:])
	defer clear(r.Secret[:])
	if err = c.PutVault(ctx, e); err != nil {
		return "", err
	}
	if err = c.SetVaultKey(k, 15*time.Minute); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	return string(raw), err
}
func (m *Manager) UnlockVault(ctx context.Context, pass, recovery string) error {
	c, err := m.current()
	if err != nil {
		return err
	}
	e, err := c.Vault(ctx)
	if err != nil {
		return err
	}
	var k vault.Key
	if recovery != "" {
		if len(recovery) > 8192 {
			return errors.New("invalid recovery package")
		}
		var r vault.Recovery
		if err = json.Unmarshal([]byte(recovery), &r); err != nil {
			return err
		}
		k, err = e.Recover(r)
		clear(r.Secret[:])
	} else {
		k, err = e.Unlock(pass)
	}
	if err != nil {
		return err
	}
	defer clear(k[:])
	err = c.SetVaultKey(k, 15*time.Minute)
	if m.durable != nil {
		m.durable.notify()
	}
	return err
}
func (m *Manager) LockVault() {
	c, err := m.current()
	if err == nil {
		c.LockVault()
	}
	if m.durable != nil {
		m.durable.mu.Lock()
		for _, a := range m.durable.active {
			if a.private {
				a.cancel()
			}
		}
		m.durable.mu.Unlock()
	}
	m.mu.Lock()
	m.catalog = map[string]api.CatalogEntry{}
	m.mu.Unlock()
}
func (m *Manager) VaultUnlocked() bool {
	c, err := m.current()
	if err != nil {
		return false
	}
	_, err = c.VaultKey()
	if err != nil {
		m.mu.Lock()
		for id, e := range m.catalog {
			if e.Mode == protocol.Private {
				delete(m.catalog, id)
			}
		}
		m.mu.Unlock()
	}
	return err == nil
}
func (m *Manager) SetPrivateMode(private bool) error {
	if private && !m.VaultUnlocked() {
		return vault.ErrLocked
	}
	m.mu.Lock()
	m.privateMode = private
	if m.client == nil {
		m.mu.Unlock()
		return errors.New("connect to a server first")
	}
	if a, ok := m.client.Account(); ok {
		for i := range m.profiles {
			if m.profiles[i].AccountID == a.AccountID && m.profiles[i].Trust.Identity.ServerID == a.ServerID {
				m.profiles[i].PrivateDefault = private
			}
		}
	}
	err := m.writeProfilesLocked()
	m.mu.Unlock()
	return err
}
func (m *Manager) RewrapVault(ctx context.Context, pass string) error {
	c, err := m.current()
	if err != nil {
		return err
	}
	k, err := c.VaultKey()
	if err != nil {
		return err
	}
	defer clear(k[:])
	e, err := c.Vault(ctx)
	if err != nil {
		return err
	}
	e, err = e.Rewrap(k, pass)
	if err != nil {
		return err
	}
	return c.PutVault(ctx, e)
}

// ConfigureVault stores only an explicit remembered unlock in the native store.
func (m *Manager) ConfigureVault(minutes int, remember bool) error {
	if minutes < 1 || minutes > 1440 {
		return errors.New("auto-lock must be 1 to 1440 minutes")
	}
	c, err := m.current()
	if err != nil {
		return err
	}
	k, err := c.VaultKey()
	if err != nil {
		return err
	}
	defer clear(k[:])
	a, _ := c.Account()
	id := credentialID("vault", string(a.ServerID), string(a.AccountID))
	if remember {
		err = keyring.Set("ServerSend-vault", id, base64.StdEncoding.EncodeToString(k[:]))
	} else {
		err = keyring.Delete("ServerSend-vault", id)
		if errors.Is(err, keyring.ErrNotFound) {
			err = nil
		}
	}
	if err != nil {
		return errors.New("native credential store unavailable; unlock manually")
	}
	if err = c.SetVaultKey(k, time.Duration(minutes)*time.Minute); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.profiles {
		if m.profiles[i].AccountID == a.AccountID && m.profiles[i].Trust.Identity.ServerID == a.ServerID {
			m.profiles[i].RememberVault = remember
			m.profiles[i].VaultMinutes = minutes
		}
	}
	return m.writeProfilesLocked()
}
func (m *Manager) UnlockRememberedVault() error {
	c, err := m.current()
	if err != nil {
		return err
	}
	a, ok := c.Account()
	if !ok {
		return vault.ErrLocked
	}
	m.mu.Lock()
	minutes := 0
	for _, p := range m.profiles {
		if p.RememberVault && p.AccountID == a.AccountID && p.Trust.Identity.ServerID == a.ServerID {
			minutes = p.VaultMinutes
		}
	}
	m.mu.Unlock()
	if minutes < 1 {
		return errors.New("remembered vault unlock is not enabled")
	}
	value, err := keyring.Get("ServerSend-vault", credentialID("vault", string(a.ServerID), string(a.AccountID)))
	if err != nil {
		return errors.New("native credential store unavailable; unlock manually")
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return errors.New("invalid remembered vault key")
	}
	var k vault.Key
	copy(k[:], raw)
	clear(raw)
	defer clear(k[:])
	e, err := c.Vault(m.base())
	if err != nil {
		return err
	}
	if err = e.VerifyKey(k); err != nil {
		return err
	}
	return c.SetVaultKey(k, time.Duration(minutes)*time.Minute)
}

func (m *Manager) PrivateMode() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.privateMode }

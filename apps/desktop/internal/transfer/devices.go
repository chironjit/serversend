package transfer

import (
	"context"
	"errors"

	"github.com/chironjit/serversend/shared/api"
)

func (m *Manager) Devices(ctx context.Context, before string) ([]api.DeviceInfo, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return nil, err
	}
	devices, err := c.Devices(ctx, before)
	return devices, message(err)
}
func (m *Manager) RevokeDevice(ctx context.Context, id string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return err
	}
	account, _ := c.Account()
	if id == string(account.DeviceID) {
		return errors.New("use Sign out to end the current device session")
	}
	return message(c.RevokeDevice(ctx, id))
}

func (m *Manager) ReplaceRecoveryCodes(ctx context.Context, password, code string) ([]string, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, err := m.current()
	if err != nil {
		return nil, err
	}
	codes, err := c.ReplaceRecoveryCodes(ctx, password, code)
	return codes, message(err)
}

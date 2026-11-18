package transfer

import (
	"errors"
	"sort"
	"strings"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

func publicAccount(c *client.Client, a api.Session) Account {
	return Account{Scope: string(a.ServerID) + "/" + string(a.AccountID), Username: a.Username, Origin: c.Origin(), ServerID: string(a.ServerID)}
}
func (m *Manager) retainClientLocked() {
	if m.clients == nil {
		m.clients = map[string]*client.Client{}
	}
	if m.client != nil {
		m.clients[m.client.Origin()] = m.client
	}
}
func (m *Manager) connectedClients() []*client.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retainClientLocked()
	keys := make([]string, 0, len(m.clients))
	for origin := range m.clients {
		keys = append(keys, origin)
	}
	sort.Strings(keys)
	result := make([]*client.Client, 0, len(keys))
	seen := map[protocol.Scope]bool{}
	for _, origin := range keys {
		c := m.clients[origin]
		a, ok := c.Account()
		scope := protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}
		if ok && !seen[scope] {
			result = append(result, c)
			seen[scope] = true
		}
	}
	return result
}
func (m *Manager) stopProfile(origin string) error {
	if m.durable == nil {
		return m.idle()
	}
	m.mu.Lock()
	m.retainClientLocked()
	c := m.clients[origin]
	delete(m.clients, origin)
	delete(m.blocked, c)
	if m.client == c {
		m.client = nil
	}
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	a, ok := c.Account()
	if ok {
		if err := m.durable.suspendScope(protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}); err != nil {
			return err
		}
	}
	c.Close()
	return nil
}

// EditProfile changes public preferences; the canonical origin keeps credential
// and installation keys stable. Reopening applies routes to the same account.
func (m *Manager) EditProfile(origin, name string, endpoints []string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	name = strings.TrimSpace(name)
	if len(name) > 100 || len(endpoints) > 7 {
		return errors.New("use a name up to 100 bytes and at most seven alternate routes")
	}
	seen := map[string]bool{origin: true}
	for _, route := range endpoints {
		if protocol.ValidateOrigin(route) != nil || seen[route] {
			return errors.New("enter distinct HTTPS route origins")
		}
		seen[route] = true
	}
	m.mu.Lock()
	found := false
	for _, p := range m.profiles {
		if p.Origin == origin {
			found = true
		}
	}
	m.mu.Unlock()
	if !found {
		return errors.New("saved server not found")
	}
	if err := m.stopProfile(origin); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.profiles {
		if m.profiles[i].Origin == origin {
			m.profiles[i].Name = name
			m.profiles[i].Endpoints = append([]string{}, endpoints...)
		}
	}
	return m.writeProfilesLocked()
}

// CombinedTransfers is bounded across connected accounts, including offline
// accounts whose clients remain signed in. Controls still require their profile.
func (m *Manager) CombinedTransfers() []Item {
	if m.durable == nil {
		return m.Transfers()
	}
	items := []Item{}
	for _, c := range m.connectedClients() {
		a, _ := c.Account()
		rows, err := m.durable.store.items(m.base(), protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID})
		if err == nil {
			items = append(items, rows...)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) > 200 {
		items = items[len(items)-200:]
	}
	return items
}

func (m *Manager) CombinedBatches() []Batch {
	if m.durable == nil {
		return m.Batches()
	}
	batches := []Batch{}
	for _, c := range m.connectedClients() {
		a, _ := c.Account()
		rows, err := m.durable.store.batches(m.base(), protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID})
		if err == nil {
			for i := range rows {
				rows[i].Scope = string(a.ServerID) + "/" + string(a.AccountID)
			}
			batches = append(batches, rows...)
		}
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].ID < batches[j].ID })
	if len(batches) > 100 {
		batches = batches[len(batches)-100:]
	}
	return batches
}
func (m *Manager) QueueActionScoped(scope string, id int, batch bool, action string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if m.durable == nil {
		return errors.New("persistent queue is unavailable")
	}
	for _, c := range m.connectedClients() {
		a, _ := c.Account()
		if scope == string(a.ServerID)+"/"+string(a.AccountID) {
			return m.queueAction(protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}, id, batch, action)
		}
	}
	return errors.New("open and sign in to this profile before managing its transfers")
}

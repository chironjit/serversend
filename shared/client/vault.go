package client

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"sync"
	"time"
)

type vaultState struct {
	timer      *time.Timer
	generation uint64
	sync.Mutex
	key   vault.Key
	scope protocol.Scope
	until time.Time
}

func (c *Client) Vault(ctx context.Context) (vault.Envelope, error) {
	var e vault.Envelope
	err := c.exchange(ctx, call{method: "GET", path: "/vault", token: c.token()}, nil, &e)
	if err == nil {
		err = e.Validate()
	}
	a, ok := c.Account()
	if err == nil && (!ok || e.Scope != (protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID})) {
		err = errors.New("vault scope mismatch")
	}
	return e, err
}
func (c *Client) PutVault(ctx context.Context, e vault.Envelope) error {
	return c.exchange(ctx, call{method: "PUT", path: "/vault", token: c.token()}, e, nil)
}
func (c *Client) SetVaultKey(k vault.Key, duration time.Duration) error {
	a, ok := c.Account()
	if !ok {
		return vault.ErrLocked
	}
	if duration <= 0 || duration > 24*time.Hour {
		return errors.New("choose auto-lock between one minute and 24 hours")
	}
	c.vault.Lock()
	defer c.vault.Unlock()
	c.vault.key = k
	c.vault.scope = protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}
	c.vault.until = time.Now().Add(duration)
	c.vault.generation++
	generation := c.vault.generation
	if c.vault.timer != nil {
		c.vault.timer.Stop()
	}
	c.vault.timer = time.AfterFunc(duration, func() {
		c.vault.Lock()
		defer c.vault.Unlock()
		if c.vault.generation == generation {
			clear(c.vault.key[:])
			c.vault.until = time.Time{}
		}
	})
	return nil
}
func (c *Client) LockVault() {
	c.vault.Lock()
	defer c.vault.Unlock()
	clear(c.vault.key[:])
	c.vault.generation++
	if c.vault.timer != nil {
		c.vault.timer.Stop()
	}
	c.vault.until = time.Time{}
}
func (c *Client) VaultKey() (vault.Key, error) {
	a, ok := c.Account()
	c.vault.Lock()
	defer c.vault.Unlock()
	if !ok || c.vault.scope != (protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}) || !time.Now().Before(c.vault.until) {
		clear(c.vault.key[:])
		return vault.Key{}, vault.ErrLocked
	}
	return c.vault.key, nil
}
func (c *Client) PrivateName(ctx context.Context, e api.CatalogEntry) (string, error) {
	k, err := c.VaultKey()
	if err != nil {
		return "", err
	}
	defer clear(k[:])
	if e.Kind == api.Directory {
		a, _ := c.Account()
		b := vault.Binding{Scope: protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}, FileID: e.ID, Kind: "directory"}
		p, err := vault.Open(k, e.Private.Payload, b.Bytes())
		if err != nil {
			return "", err
		}
		defer clear(p)
		if protocol.ValidateName(string(p)) != nil {
			return "", errors.New("invalid private directory name")
		}
		return string(p), nil
	}
	m, err := c.Manifest(ctx, e)
	if err != nil {
		return "", err
	}
	meta, key, err := vault.ReadMetadata(k, m)
	clear(key[:])
	return meta.Name, err
}
func (c *Client) CreatePrivateDirectory(ctx context.Context, parent protocol.ID, name string) (api.CatalogEntry, error) {
	k, err := c.VaultKey()
	if err != nil {
		return api.CatalogEntry{}, err
	}
	defer clear(k[:])
	if protocol.ValidateName(name) != nil {
		return api.CatalogEntry{}, errors.New("invalid private directory name")
	}
	// Resolve names locally, across all pages, without a plaintext server index.
	for cursor := api.Cursor(""); ; {
		p, err := c.CatalogFolderPage(ctx, parent, cursor)
		if err != nil {
			return api.CatalogEntry{}, err
		}
		for _, e := range p.Entries {
			if e.Mode == protocol.Private {
				n, err := c.PrivateName(ctx, e)
				if err != nil {
					return api.CatalogEntry{}, err
				}
				if n == name {
					if e.Kind != api.Directory {
						return api.CatalogEntry{}, api.Error{Code: api.Conflict, Message: "A private file uses this name"}
					}
					return e, nil
				}
			}
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	a, _ := c.Account()
	id := randomID()
	b := vault.Binding{Scope: protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}, FileID: id, Kind: "directory"}
	req := api.CreateDirectoryRequest{ID: id, ParentID: parent, Mode: protocol.Private, ExpectedHead: "0", Private: &protocol.PrivateEnvelope{Format: vault.Format, Payload: vault.Seal(k, []byte(name), b.Bytes())}}
	var e api.CatalogEntry
	err = c.exchange(ctx, call{method: "POST", path: "/directories", token: c.token()}, req, &e)
	return e, err
}

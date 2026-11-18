package client

import (
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"testing"
	"time"
)

func TestVaultAutoLockAndScope(t *testing.T) {
	c := &Client{session: api.Session{AccessToken: "local", ServerID: protocol.ID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), AccountID: protocol.ID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}}
	defer c.LockVault()
	key := vault.RandomKey()
	if err := c.SetVaultKey(key, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.vault.Lock()
		cleared := c.vault.key == (vault.Key{})
		c.vault.Unlock()
		if cleared {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("auto-lock did not erase idle key")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := c.VaultKey(); err == nil {
		t.Fatal("expired unlock accepted")
	}
	if err := c.SetVaultKey(key, time.Hour); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.session.AccountID = "cccccccccccccccccccccccccccccccc"
	c.mu.Unlock()
	if _, err := c.VaultKey(); err == nil {
		t.Fatal("cross-account unlock accepted")
	}
}

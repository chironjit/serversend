package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/storage"
)

// A pre-release object store is refused until -reset-files removes every
// uploaded file; accounts survive the reset.
func TestLegacyLayoutRequiresResetAndKeepsAccounts(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts().BootstrapAdmin(t.Context(), "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(c.DataDir, "objects", "aa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := New(t.Context(), c); !errors.Is(err, storage.ErrLegacyLayout) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("legacy layout accepted: %v", err)
	}
	if err := ResetFiles(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "objects")); !os.IsNotExist(err) {
		t.Fatalf("legacy objects kept: %v", err)
	}
	s, err = New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var admins int
	if err := s.db.QueryRow(`SELECT count(*) FROM accounts WHERE username='admin'`).Scan(&admins); err != nil || admins != 1 {
		t.Fatalf("account lost: %d %v", admins, err)
	}
}

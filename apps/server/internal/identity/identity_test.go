package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/database"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIdentityRestartAndConcurrentInitialization(t *testing.T) {
	ctx := context.Background()
	dir := privateDir(t)
	db, err := database.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := database.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	identities := make(chan Identity, 12)
	for i := range 12 {
		wg.Go(func() {
			handle := db
			if i%2 == 0 {
				handle = other
			}
			id, err := LoadOrCreate(ctx, handle)
			if err != nil {
				t.Error(err)
				return
			}
			identities <- id
		})
	}
	wg.Wait()
	close(identities)
	want := <-identities
	if len(want.ID) != 32 || want.PublicKey == ([32]byte{}) {
		t.Fatalf("invalid public identity: %#v", want)
	}
	for id := range identities {
		if id != want {
			t.Fatal("concurrent startup changed identity")
		}
	}
	db.Close()
	other.Close()
	reopened, err := database.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := LoadOrCreate(ctx, reopened); err != nil || got != want {
		t.Fatalf("restart identity=%#v error=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(dir, database.Filename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("database permissions: info=%v error=%v", info, err)
	}
	independent, err := database.Open(ctx, privateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer independent.Close()
	id, err := LoadOrCreate(ctx, independent)
	if err != nil || id.ID == want.ID || id.PublicKey == want.PublicKey {
		t.Fatalf("independent identity reused: %#v error=%v", id, err)
	}
}

func TestCorruptIdentityNeverRotates(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, privateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := LoadOrCreate(ctx, db); err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Repeat("z", 32)
	if _, err := db.Exec("UPDATE server_identity SET server_id = ?", corrupt); err != nil {
		t.Fatal(err)
	}
	if id, err := LoadOrCreate(ctx, db); err == nil || id != (Identity{}) || strings.Contains(err.Error(), corrupt) {
		t.Fatalf("corruption was not safely rejected: identity=%#v error=%v", id, err)
	}
	var saved string
	if err := db.QueryRow("SELECT server_id FROM server_identity").Scan(&saved); err != nil || saved != corrupt {
		t.Fatalf("corrupt identity was replaced: error=%v", err)
	}
}

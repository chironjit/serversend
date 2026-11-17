package database

import (
	"errors"
	"github.com/chironjit/serversend/shared/protocol"
	"os"
	"testing"
	"time"
)

func TestRecoveryKindSurvivesDatabaseRestartAndTemporaryExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	admin := "11111111111111111111111111111111"
	user := "22222222222222222222222222222222"
	now := time.Now().UTC()
	for _, row := range []struct{ id, role string }{{admin, "admin"}, {user, "user"}} {
		if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,?,?,'active',1,'hash',?)`, row.id, row.role, row.role, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRepository(db)
	if err = r.ResetPassword(t.Context(), protocol.ID(admin), 0, protocol.ID(user), "temporary", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r = NewRepository(db)
	now = now.Add(2 * time.Hour)
	if err = r.ResetTOTP(t.Context(), protocol.ID(admin), 0, protocol.ID(user), now); !errors.Is(err, ErrRecoveryPending) {
		t.Fatalf("restart/expiry cleared recovery lock: %v", err)
	}
	if err = r.ResetPassword(t.Context(), protocol.ID(admin), 0, protocol.ID(user), "replacement", now.Add(time.Hour), now); err != nil {
		t.Fatal("same-kind recovery could not be reissued", err)
	}
}

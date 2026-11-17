package database

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/sqlite"
)

func TestUpgradeInvalidatesBearerOnlySessionsAndPreservesRefreshHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/v9.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	account := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err = db.Exec(`INSERT INTO accounts(id,username,role,state,totp_enrolled,password_hash,created_at) VALUES(?,'legacy-user','user','active',1,'hash',1)`, account); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		id      string
		refresh any
	}{{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil}, {"cccccccccccccccccccccccccccccccc", "hashed-refresh"}} {
		if _, err = db.Exec(`INSERT INTO device_sessions(id,account_id,name,token_hash,auth_epoch,created_at,expires_at,refresh_hash,last_refresh,last_factor,last_seen) VALUES(?,?,'Laptop',?,0,1,9999999999999,?,1,1,1)`, d.id, account, strings.Repeat(d.id, 2), d.refresh); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sessions, revoked int
	if err = db.QueryRow(`SELECT count(*),sum(revoked_at IS NOT NULL) FROM device_sessions WHERE account_id=?`, account).Scan(&sessions, &revoked); err != nil || sessions != 2 || revoked != 1 {
		t.Fatal("legacy history/invalidation", sessions, revoked, err)
	}
	var refresh string
	if err = db.QueryRow(`SELECT refresh_hash FROM device_sessions WHERE id='cccccccccccccccccccccccccccccccc' AND revoked_at IS NULL`).Scan(&refresh); err != nil || refresh != "hashed-refresh" {
		t.Fatal("rotating credential changed", err)
	}
	if _, err = NewRepository(db).SessionByToken(context.Background(), strings.Repeat("b", 64), time.Now()); err == nil {
		t.Fatal("legacy grant remained usable")
	}
	db.Close()
	db, err = Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.QueryRow(`SELECT count(*) FROM device_sessions WHERE account_id=? AND revoked_at IS NOT NULL`, account).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatal("restarting repeated invalidation", err)
	}
}

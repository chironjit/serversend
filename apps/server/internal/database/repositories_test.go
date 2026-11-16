package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/sqlite"
	"github.com/chironjit/serversend/shared/testutil"
)

const ownerA protocol.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const ownerB protocol.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const uploadID protocol.ID = "cccccccccccccccccccccccccccccccc"

func openDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}
func accountFor(id protocol.ID) Account {
	return Account{ID: id, Username: string(id), Role: "user", Status: protocol.AccountStatus{State: protocol.PasswordRequired}, CreatedAt: time.Unix(100, 0).UTC()}
}
func uploadFor(owner protocol.ID) Upload {
	return Upload{AccountID: owner, ID: uploadID, State: protocol.UploadOpen, Mode: "standard", IdempotencyKey: "create-1", ExpectedBytes: 42, CreatedAt: time.Unix(100, 0).UTC()}
}
func mustAccount(t *testing.T, db *sql.DB, id protocol.ID) {
	t.Helper()
	if err := NewRepository(db).CreateAccount(context.Background(), accountFor(id), "opaque-encoded-hash"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoriesScopeAtomicityAndRestart(t *testing.T) {
	ctx := context.Background()
	db, dir := openDatabase(t)
	mustAccount(t, db, ownerA)
	mustAccount(t, db, ownerB)
	a := uploadFor(ownerA)
	if err := NewRepository(db).CreateUpload(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRepository(db).GetUpload(ctx, ownerB, uploadID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign lookup: %v", err)
	}
	if err := NewRepository(db).CreateUpload(ctx, a); err == nil {
		t.Fatal("duplicate idempotency key accepted")
	}
	b := uploadFor(ownerB)
	b.Mode = "private"
	if err := NewRepository(db).CreateUpload(ctx, b); err != nil {
		t.Fatal(err)
	}
	// Abort the second statement to prove upload + reservation atomicity.
	if _, err := db.Exec(`CREATE TRIGGER fail_reservation BEFORE INSERT ON reservations BEGIN SELECT RAISE(ABORT,'injected disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := a
	failed.ID = "dddddddddddddddddddddddddddddddd"
	failed.IdempotencyKey = "create-2"
	if err := NewRepository(db).CreateUpload(ctx, failed); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err := NewRepository(db).GetUpload(ctx, ownerA, failed.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partial upload left after rollback: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_reservation`); err != nil {
		t.Fatal(err)
	}
	if err := NewRepository(db).CreateUpload(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, want := range []Upload{a, b, failed} {
		got, err := NewRepository(db).GetUpload(ctx, want.AccountID, want.ID)
		if err != nil || got != want {
			t.Fatalf("restart got=%+v error=%v", got, err)
		}
	}
	got, err := NewRepository(db).GetAccount(ctx, ownerA)
	if err != nil || got != accountFor(ownerA) {
		t.Fatalf("account restart: %+v %v", got, err)
	}
	var bytes int64
	if err := db.QueryRow(`SELECT sum(bytes) FROM reservations`).Scan(&bytes); err != nil || bytes != 126 {
		t.Fatalf("reservations=%d error=%v", bytes, err)
	}
}

func TestRepositoriesRejectInvalidAndForeignReferences(t *testing.T) {
	ctx := context.Background()
	db, _ := openDatabase(t)
	for _, change := range []func(*Account){
		func(a *Account) { a.ID = "bad" }, func(a *Account) { a.Username = "" }, func(a *Account) { a.Username = " user " }, func(a *Account) { a.Role = "root" }, func(a *Account) { a.Status.State = protocol.AccountActive }, func(a *Account) { a.CreatedAt = time.Time{} },
	} {
		a := accountFor(ownerA)
		change(&a)
		if err := NewRepository(db).CreateAccount(ctx, a, "hash"); err == nil {
			t.Fatalf("invalid account accepted: %+v", a)
		}
	}
	if err := NewRepository(db).CreateAccount(ctx, accountFor(ownerA), ""); err == nil {
		t.Fatal("empty hash accepted")
	}
	mustAccount(t, db, ownerA)
	if err := NewRepository(db).CreateAccount(ctx, accountFor(ownerA), "hash"); err == nil {
		t.Fatal("duplicate account accepted")
	}
	if _, err := NewRepository(db).GetAccount(ctx, "bad"); err == nil {
		t.Fatal("invalid account ID accepted")
	}
	for _, change := range []func(*Upload){
		func(u *Upload) { u.AccountID = ownerB }, func(u *Upload) { u.ID = "../bad" }, func(u *Upload) { u.State = protocol.UploadCommitted }, func(u *Upload) { u.Mode = "other" }, func(u *Upload) { u.ExpectedBytes = -1 }, func(u *Upload) { u.IdempotencyKey = "" }, func(u *Upload) { u.CreatedAt = time.Time{} },
	} {
		u := uploadFor(ownerA)
		change(&u)
		if err := NewRepository(db).CreateUpload(ctx, u); err == nil {
			t.Fatalf("invalid upload accepted: %+v", u)
		}
	}
	if err := NewRepository(db).CreateUpload(ctx, uploadFor(ownerA)); err != nil {
		t.Fatal(err)
	}
	mustAccount(t, db, ownerB)
	for _, query := range []string{
		`INSERT INTO chunks VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccc',0,1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','dddddddddddddddddddddddddddddddd')`,
		`INSERT INTO reservations VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccc',1)`,
		`UPDATE accounts SET state='active' WHERE id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`,
		`UPDATE uploads SET state='invalid'`,
		`UPDATE uploads SET expected_bytes='not a number'`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("constraint bypass: %s", query)
		}
	}
}

func TestServerUpgradeEveryPriorSchema(t *testing.T) {
	for prior := 0; prior < len(migrations); prior++ {
		t.Run(fmt.Sprint(prior), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			db, err := sqlite.Open(context.Background(), filepath.Join(dir, Filename))
			if err != nil {
				t.Fatal(err)
			}
			if prior > 0 {
				fixture, err := os.ReadFile(fmt.Sprintf("testdata/v%d.sql", prior))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(string(fixture)); err != nil {
					t.Fatal(err)
				}
			}

			db.Close()
			db, err = Open(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if prior < 2 {
				mustAccount(t, db, ownerA)
			} else {
				account, err := NewRepository(db).GetAccount(context.Background(), ownerA)
				if err != nil || account != accountFor(ownerA) {
					t.Fatalf("prior account lost: %+v %v", account, err)
				}
				upload, err := NewRepository(db).GetUpload(context.Background(), ownerA, uploadID)
				if err != nil || upload != uploadFor(ownerA) {
					t.Fatalf("prior upload lost: %+v %v", upload, err)
				}
				var reserved int64
				if err := db.QueryRow(`SELECT bytes FROM reservations WHERE account_id=? AND upload_id=?`, ownerA, uploadID).Scan(&reserved); err != nil || reserved != 42 {
					t.Fatalf("prior reservation lost: %d %v", reserved, err)
				}
			}
			if prior >= 3 {
				c, err := NewRepository(db).CredentialsByUsername(context.Background(), string(ownerA))
				if err != nil || c.Revision != 2 || c.Epoch != 3 || c.TemporaryExpiresAt.Int64 != 200000 || c.SecondFactorBlockedUntil != 0 {
					t.Fatalf("prior credentials lost: %+v %v", c, err)
				}
				var failures, audits int
				if err := db.QueryRow(`SELECT failures FROM auth_challenges WHERE account_id=?`, ownerA).Scan(&failures); err != nil || failures != 0 {
					t.Fatalf("prior challenge lost: %d %v", failures, err)
				}
				if err := db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&audits); err != nil || audits != 1 {
					t.Fatalf("prior audit lost: %d %v", audits, err)
				}
			}
			if prior > 0 {
				var id string
				var seed []byte
				if err := db.QueryRow(`SELECT server_id,key_seed FROM server_identity`).Scan(&id, &seed); err != nil || id != string(ownerA) || fmt.Sprintf("%x", seed) != "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" {
					t.Fatalf("identity lost: %v", err)
				}
			}
		})
	}
}

func TestServerUpgradeFailurePreservesPriorVersion(t *testing.T) {
	db, dir := openDatabase(t)
	db.Close()
	// Build a prior-version fixture with a conflicting table, forcing v2 to fail.
	if err := os.Remove(filepath.Join(dir, Filename)); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), db, migrations[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE accounts(sentinel TEXT); INSERT INTO accounts VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(context.Background(), dir); err == nil || db != nil {
		t.Fatal("broken upgrade accepted")
	}
	db, err = sqlite.Open(context.Background(), filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	var sentinel string
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version=%d error=%v", version, err)
	}
	if err := db.QueryRow(`SELECT sentinel FROM accounts`).Scan(&sentinel); err != nil || sentinel != "preserved" {
		t.Fatal("prior data changed")
	}
}

func TestConcurrentRepositoryWrites(t *testing.T) {
	db, dir := openDatabase(t)
	other, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	clock := testutil.NewClock(time.Unix(200, 0))
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			a := accountFor(protocol.ID(fmt.Sprintf("%032x", i+1)))
			a.CreatedAt = clock.Now()
			handle := db
			if i%2 == 1 {
				handle = other
			}
			failures <- NewRepository(handle).CreateAccount(context.Background(), a, "hash")
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}

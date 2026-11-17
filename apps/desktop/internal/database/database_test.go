package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/sqlite"
)

const serverA protocol.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const serverB protocol.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const accountA protocol.ID = "cccccccccccccccccccccccccccccccc"
const accountB protocol.ID = "dddddddddddddddddddddddddddddddd"
const jobID protocol.ID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

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
func profileFor(scope protocol.Scope) Profile {
	return Profile{Scope: scope, DisplayName: "Home", IdentityKey: make([]byte, 32), Endpoints: []string{"https://home.example:8443"}}
}
func jobFor(scope protocol.Scope) Job {
	return Job{Key: protocol.ResourceKey{Scope: scope, ID: jobID}, State: protocol.Queued, Mode: "standard", CreatedAt: time.Unix(100, 0).UTC()}
}

func TestDesktopScopeIsolationAndRestart(t *testing.T) {
	ctx := context.Background()
	db, dir := openDatabase(t)
	scopes := []protocol.Scope{{ServerID: serverA, AccountID: accountA}, {ServerID: serverB, AccountID: accountA}, {ServerID: serverA, AccountID: accountB}}
	for _, scope := range scopes {
		if err := NewRepository(db).CreateProfile(ctx, profileFor(scope)); err != nil {
			t.Fatal(err)
		}
		if err := NewRepository(db).CreateJob(ctx, jobFor(scope)); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewRepository(db).TransitionJob(ctx, jobFor(scopes[0]).Key, protocol.Queued, protocol.Running); err != nil {
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
	for i, scope := range scopes {
		got, err := NewRepository(db).GetProfile(ctx, scope)
		if err != nil || !reflect.DeepEqual(got, profileFor(scope)) {
			t.Fatalf("profile=%+v error=%v", got, err)
		}
		jobs, err := NewRepository(db).ListJobs(ctx, scope, "", 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("jobs=%+v error=%v", jobs, err)
		}
		want := jobFor(scope)
		if i == 0 {
			want.State = protocol.Running
		}
		if jobs[0] != want {
			t.Fatalf("scoped state=%+v want=%+v", jobs[0], want)
		}
	}
	foreign := protocol.Scope{ServerID: serverB, AccountID: accountB}
	if _, err := NewRepository(db).GetProfile(ctx, foreign); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign profile returned: %v", err)
	}
	jobs, err := NewRepository(db).ListJobs(ctx, foreign, "", 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("foreign jobs=%+v error=%v", jobs, err)
	}
	if err := NewRepository(db).TransitionJob(ctx, jobFor(foreign).Key, protocol.Queued, protocol.Running); !errors.Is(err, ErrJobConflict) {
		t.Fatalf("foreign mutation: %v", err)
	}
}

func TestProfileTrustAndAtomicRoutes(t *testing.T) {
	ctx := context.Background()
	db, _ := openDatabase(t)
	scope := protocol.Scope{ServerID: serverA, AccountID: accountA}
	for _, change := range []func(*Profile){
		func(p *Profile) { p.Scope.ServerID = "invalid" }, func(p *Profile) { p.IdentityKey = nil }, func(p *Profile) { p.DisplayName = "" }, func(p *Profile) { p.Endpoints = nil }, func(p *Profile) { p.Endpoints = []string{"http://home.example"} }, func(p *Profile) { p.Endpoints = append(p.Endpoints, p.Endpoints[0]) },
	} {
		p := profileFor(scope)
		change(&p)
		if err := NewRepository(db).CreateProfile(ctx, p); err == nil {
			t.Fatalf("invalid profile accepted: %+v", p)
		}
		if _, err := NewRepository(db).GetProfile(ctx, scope); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("partial profile left: %v", err)
		}
	}
	p := profileFor(scope)
	if err := NewRepository(db).CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	replaced := profileFor(scope)
	replaced.IdentityKey[0] = 1
	if err := NewRepository(db).CreateProfile(ctx, replaced); err == nil {
		t.Fatal("trust pin overwritten")
	}
	got, err := NewRepository(db).GetProfile(ctx, scope)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatal("duplicate changed trust pin")
	}
}

func TestJobValidationPaginationAndRacingTransition(t *testing.T) {
	ctx := context.Background()
	db, dir := openDatabase(t)
	scope := protocol.Scope{ServerID: serverA, AccountID: accountA}
	if err := NewRepository(db).CreateJob(ctx, jobFor(scope)); err == nil {
		t.Fatal("foreign key bypassed")
	}
	if err := NewRepository(db).CreateProfile(ctx, profileFor(scope)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Job){func(j *Job) { j.Key.ID = "bad" }, func(j *Job) { j.State = protocol.Complete }, func(j *Job) { j.Mode = "other" }, func(j *Job) { j.CreatedAt = time.Time{} }} {
		j := jobFor(scope)
		change(&j)
		if err := NewRepository(db).CreateJob(ctx, j); err == nil {
			t.Fatalf("invalid job accepted: %+v", j)
		}
	}
	for i := 1; i <= 5; i++ {
		j := jobFor(scope)
		j.Key.ID = protocol.ID(fmt.Sprintf("%032x", i))
		if err := NewRepository(db).CreateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	var after protocol.ID
	seen := 0
	for {
		jobs, err := NewRepository(db).ListJobs(ctx, scope, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 0 {
			break
		}
		if len(jobs) > 2 {
			t.Fatal("page exceeded limit")
		}
		for _, j := range jobs {
			seen++
			if j.Key.ID != protocol.ID(fmt.Sprintf("%032x", seen)) {
				t.Fatal("pagination gap or repeat")
			}
		}
		after = jobs[len(jobs)-1].Key.ID
	}
	if seen != 5 {
		t.Fatalf("seen=%d", seen)
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := NewRepository(db).ListJobs(ctx, scope, "", limit); err == nil {
			t.Fatal("invalid page size accepted")
		}
	}
	if _, err := NewRepository(db).ListJobs(ctx, scope, "bad", 1); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	key := jobFor(scope).Key
	key.ID = protocol.ID(fmt.Sprintf("%032x", 1))
	if err := NewRepository(db).TransitionJob(ctx, key, protocol.Queued, protocol.Complete); err == nil {
		t.Fatal("state machine bypassed")
	}
	other, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, handle := range []*sql.DB{db, other} {
		wg.Go(func() { results <- NewRepository(handle).TransitionJob(ctx, key, protocol.Queued, protocol.Running) })
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrJobConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestDesktopSchemaUpgradeAndFutureGuard(t *testing.T) {
	ctx := context.Background()
	db, dir := openDatabase(t)
	if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(ctx, dir); db != nil || !errors.Is(err, sqlite.ErrFutureSchema) {
		t.Fatalf("future guard: %v", err)
	}
	// Upgrade an empty desktop database through every current migration.
	fresh := t.TempDir()
	if err := os.Chmod(fresh, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, filepath.Join(fresh, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version=%d error=%v", version, err)
	}
	if _, err := Open(ctx, filepath.Join(fresh, "missing")); err == nil {
		t.Fatal("missing path accepted")
	}
}

func TestDesktopMigrationFailureAndOwnedTaskReferences(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE jobs(sentinel TEXT); INSERT INTO jobs VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(ctx, dir); db != nil || err == nil {
		t.Fatal("migration failure ignored")
	}
	db, err = sqlite.Open(ctx, filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatal("failed migration advanced version")
	}
	if _, err := db.Exec("SELECT * FROM profiles"); err == nil {
		t.Fatal("failed migration left partial schema")
	}
	db.Close()
	db, _ = openDatabase(t)
	scope := protocol.Scope{ServerID: serverA, AccountID: accountA}
	if err := NewRepository(db).CreateProfile(ctx, profileFor(scope)); err != nil {
		t.Fatal(err)
	}
	if err := NewRepository(db).CreateJob(ctx, jobFor(scope)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO file_tasks(server_id,account_id,job_id,id,source_path) VALUES(?,?,?,?,?)`, serverA, accountB, jobID, jobID, "local"); err == nil {
		t.Fatal("foreign task linked to job")
	}
	if _, err := db.Exec(`INSERT INTO file_tasks(server_id,account_id,job_id,id,source_path) VALUES(?,?,?,?,?)`, serverA, accountA, jobID, jobID, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO chunk_acknowledgements VALUES(?,?,?,?,?,?,?)`, serverB, accountA, jobID, jobID, 0, 1, fmt.Sprintf("%064x", 1)); err == nil {
		t.Fatal("foreign acknowledgement linked to task")
	}
	if _, err := db.Exec(`INSERT INTO chunk_acknowledgements VALUES(?,?,?,?,?,?,?)`, serverA, accountA, jobID, jobID, 0, 1, fmt.Sprintf("%064x", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE profiles SET catalog_revision='18446744073709551615'`); err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{"01", "-1", "18446744073709551616"} {
		if _, err := db.Exec(`UPDATE profiles SET catalog_revision=?`, rev); err == nil {
			t.Fatalf("invalid revision=%s", rev)
		}
	}
}

func TestDesktopV1QueueUpgradePreservesFoundation(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, db, []sqlite.Migration{{Version: 1, SQL: initialSchema}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO profiles(server_id,account_id,display_name,identity_key) VALUES('11111111111111111111111111111111','22222222222222222222222222222222','preserved',zeroblob(32))`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow(`SELECT display_name FROM profiles`).Scan(&name); err != nil || name != "preserved" {
		t.Fatalf("v1 row lost: %q %v", name, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM transfer_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("queue migration: %d %v", count, err)
	}
}

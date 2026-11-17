package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

const owner protocol.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const serverID protocol.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func fixture(t *testing.T, policy StoragePolicy) (*Service, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	data := filepath.Join(root, "data")
	for _, dir := range []string{config, data} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := database.NewRepository(db)
	err = repo.CreateAccount(context.Background(), database.Account{ID: owner, Username: "test", Role: "user", Status: protocol.AccountStatus{State: protocol.PasswordRequired}, CreatedAt: time.Now()}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(repo, store, serverID, protocol.TransferLimits{MinChunkBytes: 1 << 20, MaxChunkBytes: 8 << 20, MaxRequestBytes: 8 << 20, MaxManifestBytes: 1 << 20, MaxChunks: 1 << 20}, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, data
}

func request(n int64) api.CreateUploadRequest {
	return api.CreateUploadRequest{FileID: newID(), VersionID: newID(), Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: 1, TransmittedBytes: n, ManifestBytes: 1024}
}
func digest(b []byte) protocol.Digest {
	sum := sha256.Sum256(b)
	return protocol.Digest(hex.EncodeToString(sum[:]))
}
func create(t *testing.T, s *Service, n int64) api.Upload {
	t.Helper()
	u, _, err := s.Create(context.Background(), owner, string(newID()), request(n))
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func code(t *testing.T, err error, want api.ErrorCode) {
	t.Helper()
	var typed api.Error
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}

func TestConcurrentReservationsAndIdempotency(t *testing.T) {
	s, db, _ := fixture(t, StoragePolicy{GlobalBytes: 10, UserBytes: 10})
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, _, err := s.Create(context.Background(), owner, string(newID()), request(6))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			code(t, err, api.CapacityExhausted)
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d", success)
	}
	var id protocol.ID
	var key string
	if err := db.QueryRow(`SELECT id,idempotency_key FROM uploads`).Scan(&id, &key); err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(context.Background(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	got, created, err := s.Create(context.Background(), owner, key, original.CreateUploadRequest)
	if err != nil || created || got.ID != id {
		t.Fatalf("replay %v %v", created, err)
	}
	changed := original.CreateUploadRequest
	changed.TransmittedBytes++
	_, _, err = s.Create(context.Background(), owner, key, changed)
	code(t, err, api.Conflict)
}

func TestCancelExpiryAndReservationRelease(t *testing.T) {
	ctx := context.Background()
	s, db, _ := fixture(t, StoragePolicy{UserBytes: 4})
	u := create(t, s, 4)
	b := []byte("data")
	ack, created, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b))
	if err != nil || !created {
		t.Fatal(err)
	}
	again, created, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b))
	if err != nil || created || again != ack {
		t.Fatalf("retry %v", err)
	}
	_, _, err = s.PutChunk(ctx, owner, u.ID, 0, 4, digest([]byte("else")), bytes.NewReader([]byte("else")))
	code(t, err, api.Conflict)
	if err = s.Cancel(ctx, owner, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Cancel(ctx, owner, u.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b))
	code(t, err, api.UploadGone)
	v := create(t, s, 4)
	s.now = func() time.Time { return v.ExpiresAt.Add(time.Second) }
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	for _, table := range []string{"chunks", "reservations", "deleted_objects"} {
		if err = db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	got, err := s.Get(ctx, owner, v.ID)
	if err != nil || got.State != protocol.UploadExpired {
		t.Fatalf("expired %+v %v", got, err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReservationBoundsAndLowDisk(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	u := create(t, s, 1)
	b := []byte("too big")
	_, _, err := s.PutChunk(context.Background(), owner, u.ID, 0, int64(len(b)), digest(b), bytes.NewReader(b))
	code(t, err, api.InvalidRequest)
	s.policy.MinFreeBytes = 1 << 62
	_, _, err = s.Create(context.Background(), owner, string(newID()), request(1))
	code(t, err, api.CapacityExhausted)
	_, _, err = s.PutChunk(context.Background(), owner, u.ID, 0, 1, digest(b[:1]), bytes.NewReader(b[:1]))
	code(t, err, api.CapacityExhausted)
}

func TestRestartReconcilesPublicationBeforeMetadata(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	u := create(t, s, 4)
	b := []byte("data")
	if _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	// A child exits without defers after publication, before acknowledgement.
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashObjectHelper$")
	cmd.Env = append(os.Environ(), "SERVERSEND_CRASH_DATA="+data)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("helper did not crash")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 73 {
			t.Fatalf("%v: %s", err, output)
		}
	}
	store, err := storage.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(database.NewRepository(db), store, serverID, s.limits, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(data, "objects", "cc", "cccccccccccccccccccccccccccccccc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan retained: %v", err)
	}
	chunks, err := s.repo.StoredChunks(ctx, owner, u.ID)
	if err != nil || len(chunks) != 1 {
		t.Fatal(err)
	}
	if err = store.VerifyExists(chunks[0].ObjectID, 4); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(chunks[0].ObjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = New(database.NewRepository(db), store, serverID, s.limits, nil); err == nil {
		t.Fatal("missing acknowledged bytes accepted")
	}
}
func TestCrashObjectHelper(t *testing.T) {
	data := os.Getenv("SERVERSEND_CRASH_DATA")
	if data == "" {
		return
	}
	store, err := storage.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	b := []byte("orphan")
	if err = store.Put("cccccccccccccccccccccccccccccccc", bytes.NewReader(b), int64(len(b)), digest(b)); err != nil {
		t.Fatal(err)
	}
	os.Exit(73)
}

func TestMetadataFailureLeavesRecoverableOrphan(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	u := create(t, s, 4)
	if _, err := db.Exec(`CREATE TRIGGER fail_chunk BEFORE INSERT ON chunks BEGIN SELECT RAISE(ABORT,'injected metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	b := []byte("data")
	if _, _, err := s.PutChunk(context.Background(), owner, u.ID, 0, 4, digest(b), bytes.NewReader(b)); err == nil {
		t.Fatal("failure ignored")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_chunk`); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(database.NewRepository(db), store, serverID, s.limits, nil); err != nil {
		t.Fatal(err)
	}
	entries := 0
	filepath.WalkDir(filepath.Join(data, "objects"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			entries++
		}
		return err
	})
	if entries != 0 {
		t.Fatal(fmt.Sprint("orphans: ", entries))
	}
}

func TestCancelFinalizeRaceRetainsOnlyWinningState(t *testing.T) {
	for range 10 {
		s, db, _ := fixture(t, StoragePolicy{UserBytes: 4})
		ctx := context.Background()
		r := request(4)
		b := []byte("data")
		manifest := protocol.Manifest{SchemaVersion: 1, Scope: protocol.Scope{ServerID: serverID, AccountID: owner}, FileID: r.FileID, VersionID: r.VersionID, Mode: protocol.Standard, TotalBytes: 4, Chunks: []protocol.Chunk{{Index: 0, Bytes: 4, SHA256: digest(b)}}, Standard: &protocol.StandardMetadata{Name: "file"}}
		body, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		r.ManifestBytes = int64(len(body))
		u, _, err := s.Create(ctx, owner, string(newID()), r)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
		var finalErr, cancelErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, finalErr = s.Finalize(ctx, owner, u.ID, body) })
		wg.Go(func() { cancelErr = s.Cancel(ctx, owner, u.ID) })
		wg.Wait()
		got, err := s.Get(ctx, owner, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch got.State {
		case protocol.UploadCommitted:
			_, retryCreated, retryErr := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b))
			if retryErr != nil || retryCreated {
				t.Fatalf("committed chunk replay: %v", retryErr)
			}
			_, _, quotaErr := s.Create(ctx, owner, string(newID()), request(1))
			code(t, quotaErr, api.CapacityExhausted)
			if finalErr != nil {
				t.Fatal(finalErr)
			}
			code(t, cancelErr, api.Conflict)
			if _, err = s.Finalize(ctx, owner, u.ID, body); err != nil {
				t.Fatal(err)
			}
			f, _, err := s.OpenChunk(ctx, owner, r.FileID, r.VersionID, 0)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		case protocol.UploadCancelled:
			if cancelErr != nil {
				t.Fatal(cancelErr)
			}
			code(t, finalErr, api.UploadGone)
		default:
			t.Fatalf("unexpected state %s", got.State)
		}
		var reserved int
		if err = db.QueryRow(`SELECT count(*) FROM reservations`).Scan(&reserved); err != nil || reserved != 0 {
			t.Fatalf("reservation leaked %d %v", reserved, err)
		}
	}
}

func TestCancellationWaitsForPublication(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	ctx := context.Background()
	u := create(t, s, 4)
	b := []byte("data")
	entered := make(chan struct{})
	release := make(chan struct{})
	writeDone := make(chan error, 1)
	cancelDone := make(chan error, 1)
	reader := &blockingReader{entered: entered, release: release, body: bytes.NewReader(b)}
	go func() { _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), reader); writeDone <- err }()
	<-entered
	go func() { cancelDone <- s.Cancel(ctx, owner, u.ID) }()
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	chunks, err := s.repo.StoredChunks(ctx, owner, u.ID)
	if err != nil || len(chunks) != 0 {
		t.Fatalf("chunks %+v %v", chunks, err)
	}
}

type blockingReader struct {
	entered, release chan struct{}
	body             *bytes.Reader
	once             sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.body.Read(p)
}

func TestQuotaScopesAndForeignCancellation(t *testing.T) {
	for _, policy := range []StoragePolicy{{UserBytes: 4}, {GlobalBytes: 4}} {
		s, _, _ := fixture(t, policy)
		ctx := context.Background()
		other := protocol.ID("dddddddddddddddddddddddddddddddd")
		if err := s.repo.CreateAccount(ctx, database.Account{ID: other, Username: "other", Role: "user", Status: protocol.AccountStatus{State: protocol.PasswordRequired}, CreatedAt: time.Now()}, "hash"); err != nil {
			t.Fatal(err)
		}
		u := create(t, s, 4)
		code(t, s.Cancel(ctx, other, u.ID), api.NotFound)
		_, _, err := s.Create(ctx, other, string(newID()), request(4))
		if policy.UserBytes > 0 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			code(t, err, api.CapacityExhausted)
		}
	}
}

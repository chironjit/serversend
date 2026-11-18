package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
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
	s, db, data := fixture(t, StoragePolicy{UserBytes: 4})
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
	assertNoObjects(t, data)
	if pending, _, err := s.repo.Journal(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("unapplied changes %v %v", pending, err)
	}
	for _, table := range []string{"chunks", "reservations"} {
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

// Startup finishes interrupted filesystem changes before clearing temporary
// files, keeps open uploads, removes orphaned upload files and refuses
// missing acknowledged contents.
func TestRestartReplaysJournalAndRefusesMissingContents(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	b := []byte("data")
	open, m := createFile(t, s, "kept.bin", b)
	if _, _, err := s.PutChunk(ctx, owner, open.ID, 0, 4, digest(b), bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	internal := filepath.Join(data, ".serversend")
	orphan := filepath.Join(internal, "uploads", string(newID()))
	pending := filepath.Join(internal, "tmp", "pending")
	for _, path := range []string{orphan, pending} {
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A crash after commit leaves a journaled move from the temporary area.
	if _, err := db.Exec(`INSERT INTO storage_journal(kind,source,target) VALUES('move','.serversend/tmp/pending','test/Recovered/file.bin')`); err != nil {
		t.Fatal(err)
	}
	restart := func() error {
		store, err := storage.Open(data)
		if err != nil {
			return err
		}
		_, err = New(database.NewRepository(db), store, serverID, s.limits, nil)
		return err
	}
	if err := restart(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(data, "test", "Recovered", "file.bin")); err != nil || string(got) != "data" {
		t.Fatalf("journaled move not replayed: %v", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned upload file retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(internal, "uploads", string(open.ID))); err != nil {
		t.Fatalf("open upload file removed: %v", err)
	}
	var journal int
	if err := db.QueryRow(`SELECT count(*) FROM storage_journal`).Scan(&journal); err != nil || journal != 0 {
		t.Fatalf("journal not cleared: %d %v", journal, err)
	}
	if _, err := s.Finalize(ctx, owner, open.ID, m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(data, "test", "kept.bin")); err != nil {
		t.Fatal(err)
	}
	if err := restart(); err == nil {
		t.Fatal("missing acknowledged contents accepted")
	}
}

// Unacknowledged bytes stay invisible and a retry overwrites them.
func TestMetadataFailureLeavesChunkRetryable(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	b := []byte("data")
	u, m := createFile(t, s, "retried.bin", b)
	if _, err := db.Exec(`CREATE TRIGGER fail_chunk BEFORE INSERT ON chunks BEGIN SELECT RAISE(ABORT,'injected metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest([]byte("xxxx")), bytes.NewReader([]byte("xxxx"))); err == nil {
		t.Fatal("failure ignored")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_chunk`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b)); err != nil || !created {
		t.Fatalf("retry: %v", err)
	}
	if _, err := s.Finalize(ctx, owner, u.ID, m); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(data, "test", "retried.bin")); err != nil || string(got) != "data" {
		t.Fatalf("published %q: %v", got, err)
	}
}

// createFile opens an upload for data with its exact manifest.
func createFile(t *testing.T, s *Service, name string, data []byte) (api.Upload, []byte) {
	t.Helper()
	file := batchFile(t, name, data)
	u, _, err := s.Create(context.Background(), owner, file.IdempotencyKey, file.CreateUploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	return u, file.Manifest
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

// Cancellation does not wait for a chunk still being received. The late
// chunk is refused and its unrecorded object removed, so nothing leaks.
func TestCancellationRefusesInFlightChunk(t *testing.T) {
	s, _, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	u := create(t, s, 4)
	b := []byte("data")
	entered := make(chan struct{})
	release := make(chan struct{})
	writeDone := make(chan error, 1)
	reader := &blockingReader{entered: entered, release: release, body: bytes.NewReader(b)}
	go func() { _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), reader); writeDone <- err }()
	<-entered
	if err := s.Cancel(ctx, owner, u.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	code(t, <-writeDone, api.UploadGone)
	chunks, err := s.repo.StoredChunks(ctx, owner, u.ID)
	if err != nil || len(chunks) != 0 {
		t.Fatalf("chunks %+v %v", chunks, err)
	}
	assertNoObjects(t, data)
}

// A slow client must not hold back other uploads.
func TestStalledChunkDoesNotBlockOtherUploads(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	ctx := context.Background()
	slow, fast := create(t, s, 4), create(t, s, 4)
	b := []byte("data")
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go s.PutChunk(ctx, owner, slow.ID, 0, 4, digest(b), &blockingReader{entered: entered, release: release, body: bytes.NewReader(b)})
	<-entered
	done := make(chan error, 1)
	go func() { _, _, err := s.PutChunk(ctx, owner, fast.ID, 0, 4, digest(b), bytes.NewReader(b)); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled chunk body blocked another upload")
	}
}

// assertNoObjects checks that no upload or batch bytes remain.
func assertNoObjects(t *testing.T, data string) {
	t.Helper()
	for _, area := range []string{"uploads", "tmp"} {
		walkObjects(t, filepath.Join(data, ".serversend", area), func() { t.Error("unexpected file in", area) })
	}
}

func walkObjects(t *testing.T, dir string, visit func()) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			visit()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
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

// A stored file whose size changed on the data volume no longer blocks
// startup; it is reported and its remaining bytes stay downloadable.
func TestChangedStoredFileStaysDownloadable(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	b := []byte("data")
	u, m := createFile(t, s, "edited.bin", b)
	if _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 4, digest(b), bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	result, err := s.Finalize(ctx, owner, u.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "test", "edited.bin"), []byte("da"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(database.NewRepository(db), store, serverID, s.limits, nil)
	if err != nil {
		t.Fatalf("changed file blocked startup: %v", err)
	}
	if count, paths := restarted.ChangedFiles(); count != 1 || len(paths) != 1 || paths[0] != "test/edited.bin" {
		t.Fatalf("changed files: %d %v", count, paths)
	}
	reader, chunk, err := restarted.OpenChunk(ctx, owner, result.FileID, result.VersionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != "da" || chunk.Bytes != 2 || chunk.SHA256 != digest(b) {
		t.Fatalf("served %q as %+v: %v", got, chunk, err)
	}
}

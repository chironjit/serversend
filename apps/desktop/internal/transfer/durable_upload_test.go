package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

type durableUploadFixture struct {
	mu          sync.Mutex
	intent      api.CreateUploadRequest
	upload      protocol.ID
	key         string
	state       protocol.UploadState
	chunks      map[int64][]byte
	puts        map[int64]int
	admissions  int
	blockSecond bool
	blocked     chan struct{}
}

func (f *durableUploadFixture) handler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, api.BasePath)
	reply := func(value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	if path == "/sessions" {
		reply(api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "memory-session"})
		return
	}
	if path == "/capabilities" {
		reply(api.Capabilities{ProtocolVersions: []protocol.Version{protocol.Version1}, Transports: []api.Transport{api.HTTP2}, Limits: protocol.TransferLimits{MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536, MaxManifestBytes: 65536, MaxChunks: 100}})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case path == "/catalog/target":
		reply(struct {
			Entry *api.CatalogEntry `json:"entry"`
		}{nil})
	case path == "/uploads" && r.Method == "POST":
		var intent api.CreateUploadRequest
		if json.NewDecoder(r.Body).Decode(&intent) != nil {
			w.WriteHeader(400)
			return
		}
		key := r.Header.Get(api.IdempotencyKeyHeader)
		if f.key != "" && key != f.key {
			w.WriteHeader(409)
			return
		}
		if f.key == "" {
			f.admissions++
			f.key = key
			f.upload = "33333333333333333333333333333333"
			f.intent = intent
			f.state = protocol.UploadOpen
			f.chunks = map[int64][]byte{}
		}
		reply(api.Upload{ID: f.upload, CreateUploadRequest: f.intent, State: f.state, ExpiresAt: time.Now().Add(time.Hour)})
	case strings.Contains(path, "/chunks/"):
		index, _ := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
		f.puts[index]++
		if index == 1 && f.blockSecond {
			f.blockSecond = false
			close(f.blocked)
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != r.Header.Get(api.ChunkDigestHeader) {
			w.WriteHeader(400)
			return
		}
		f.chunks[index] = body
		reply(struct{}{})
	case strings.HasSuffix(path, "/missing"):
		missing := api.MissingChunks{Page: api.Page{Snapshot: "55555555555555555555555555555555"}, UploadID: f.upload, ChunkCount: f.intent.ChunkCount, Missing: []api.ChunkRange{}}
		for i := int64(0); i < f.intent.ChunkCount; i++ {
			if f.chunks[i] == nil {
				if len(missing.Missing) > 0 && missing.Missing[len(missing.Missing)-1].End == i {
					missing.Missing[len(missing.Missing)-1].End++
				} else {
					missing.Missing = append(missing.Missing, api.ChunkRange{Start: i, End: i + 1})
				}
			}
		}
		reply(missing)
	case strings.HasSuffix(path, "/finalize"):
		var manifest protocol.Manifest
		if json.NewDecoder(r.Body).Decode(&manifest) != nil {
			w.WriteHeader(400)
			return
		}
		for _, chunk := range manifest.Chunks {
			sum := sha256.Sum256(f.chunks[chunk.Index])
			if int64(len(f.chunks[chunk.Index])) != chunk.Bytes || hex.EncodeToString(sum[:]) != string(chunk.SHA256) {
				w.WriteHeader(409)
				return
			}
		}
		f.state = protocol.UploadCommitted
		reply(api.FinalizeResult{UploadID: f.upload, FileID: f.intent.FileID, VersionID: f.intent.VersionID, State: f.state, CatalogRevision: "1", HeadRevision: "1"})
	case r.Method == "DELETE":
		if f.state == protocol.UploadCommitted {
			w.WriteHeader(409)
			reply(api.ErrorResponse{Error: api.Error{Code: api.Conflict, Message: "Already committed"}})
			return
		}
		f.state = protocol.UploadCancelled
		reply(struct{}{})
	case r.Method == "GET":
		reply(api.Upload{ID: f.upload, CreateUploadRequest: f.intent, State: f.state, ExpiresAt: time.Now().Add(time.Hour)})
	default:
		w.WriteHeader(404)
	}
}
func durableFixtureClient(t *testing.T, origin, pin string) *client.Client {
	t.Helper()
	c, err := client.New(origin, pin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if _, err = c.SignIn(t.Context(), "challenge", "123456", "test-device"); err != nil {
		t.Fatal(err)
	}
	return c
}
func durableFixtureQueue(t *testing.T, store *durableStore, c *client.Client) *durableQueue {
	t.Helper()
	return &durableQueue{manager: &Manager{ctx: t.Context(), client: c, emit: func(Item) {}}, store: store}
}

func TestDurableUploadSurvivesProcessKill(t *testing.T) {
	if os.Getenv("SERVERSEND_UPLOAD_KILL_HELPER") == "1" {
		store, err := openDurable(t.Context(), os.Getenv("SERVERSEND_UPLOAD_TEST_STORE"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.db.Close()
		c := durableFixtureClient(t, os.Getenv("SERVERSEND_UPLOAD_TEST_ORIGIN"), os.Getenv("SERVERSEND_UPLOAD_TEST_PIN"))
		session, _ := c.Account()
		task, err := store.claim(t.Context(), protocol.Scope{ServerID: session.ServerID, AccountID: session.AccountID})
		if err != nil {
			t.Fatal(err)
		}
		durableFixtureQueue(t, store, c).run(t.Context(), c, task)
		t.Fatal("helper unexpectedly completed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	expected := append(bytes.Repeat([]byte("a"), 4096), bytes.Repeat([]byte("b"), 8192)...)
	if err = os.WriteFile(source, expected, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	scope := protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}
	if _, err = store.create(t.Context(), scope, "durable selection", []durableTask{{Kind: "upload", Name: "source", Total: int64(len(expected)), Payload: durablePayload{Path: source}}}); err != nil {
		t.Fatal(err)
	}
	store.db.Close()
	fixture := &durableUploadFixture{puts: map[int64]int{}, blockSecond: true, blocked: make(chan struct{})}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.handler))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	fingerprint := sha256.Sum256(server.Certificate().Raw)
	pin := hex.EncodeToString(fingerprint[:])
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestDurableUploadSurvivesProcessKill$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "SERVERSEND_UPLOAD_KILL_HELPER=1", "SERVERSEND_UPLOAD_TEST_STORE="+root, "SERVERSEND_UPLOAD_TEST_ORIGIN="+server.URL, "SERVERSEND_UPLOAD_TEST_PIN="+pin)
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	select {
	case <-fixture.blocked:
	case <-time.After(15 * time.Second):
		t.Fatalf("helper did not checkpoint chunk: %s", logs.String())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	store, err = openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	task, err := store.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if task.Payload.Upload == nil || task.Payload.Upload.UploadID == "" || task.Payload.Upload.AcknowledgedBytes != 4096 {
		t.Fatalf("lost checkpoint: %+v", task.Payload.Upload)
	}
	c := durableFixtureClient(t, server.URL, pin)
	durableFixtureQueue(t, store, c).run(t.Context(), c, task)
	items, err := store.items(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "done" {
		t.Fatalf("resumed items: %+v", items)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.admissions != 1 || fixture.puts[0] != 1 {
		t.Fatalf("completed work repeated: admissions=%d puts=%v", fixture.admissions, fixture.puts)
	}
	var got []byte
	for index := int64(0); index < fixture.intent.ChunkCount; index++ {
		got = append(got, fixture.chunks[index]...)
	}
	if !bytes.Equal(got, expected) {
		t.Fatal("resumed upload bytes differ")
	}
}

func TestDurableControlsAndScopeSurviveReopen(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	scope := protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}
	other := protocol.Scope{ServerID: scope.ServerID, AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	store, err := openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.create(t.Context(), scope, "controls", []durableTask{{Kind: "upload", Name: "source", Payload: durablePayload{Path: filepath.Join(root, "source")}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.control(t.Context(), other, task.ID, false, "pause"); err == nil {
		t.Fatal("foreign scope control accepted")
	}
	items, err := store.items(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].State != "active" {
		t.Fatal("foreign scope controlled task")
	}
	if err = store.control(t.Context(), scope, batch, true, "pause"); err != nil {
		t.Fatal(err)
	}
	store.db.Close()
	store, err = openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	items, err = store.items(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].State != "paused" {
		t.Fatalf("pause lost: %+v", items)
	}
	if err = store.control(t.Context(), scope, batch, true, "cancel"); err != nil {
		t.Fatal(err)
	}
	items, err = store.items(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "cancel_pending" {
		t.Fatalf("cancel intent: %s", fmt.Sprint(items))
	}
}

func TestDurableFolderScanReenumeratesWithoutDuplicatingCompletedWork(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "tree")
	if err = os.MkdirAll(filepath.Join(tree, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		if err = os.WriteFile(filepath.Join(tree, fmt.Sprintf("file-%03d", i)), []byte("contents"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	linked := os.Symlink("file-000", filepath.Join(tree, "link")) == nil
	fixture := &durableUploadFixture{puts: map[int64]int{}}
	var dirMu sync.Mutex
	dirs := map[string]protocol.ID{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.BasePath+"/directories" {
			fixture.handler(w, r)
			return
		}
		var request api.CreateDirectoryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		dirMu.Lock()
		defer dirMu.Unlock()
		key := string(request.ParentID) + "/" + request.Standard.Name
		folder := dirs[key]
		if folder == "" {
			folder = protocol.ID(fmt.Sprintf("%032x", len(dirs)+100))
			dirs[key] = folder
		}
		_ = json.NewEncoder(w).Encode(api.CatalogEntry{ID: folder, ParentID: request.ParentID, Kind: api.Directory, Mode: protocol.Standard, HeadRevision: "1", Standard: request.Standard})
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().Raw)
	c := durableFixtureClient(t, server.URL, hex.EncodeToString(sum[:]))
	scope := protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}
	store, err := openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.create(t.Context(), scope, "large folder", []durableTask{{Kind: "scan_upload", Name: "tree", Payload: durablePayload{Path: tree}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	// Interrupt the second discovery transaction. The first 128 children have
	// committed, while the scanner still looks active as it would after a crash.
	trigger := fmt.Sprintf(`CREATE TRIGGER interrupt_scan BEFORE INSERT ON transfer_tasks WHEN NEW.batch_id=%d AND (SELECT count(*) FROM transfer_tasks WHERE batch_id=%d)>=129 BEGIN SELECT RAISE(FAIL,'scan interrupted'); END`, batch, batch)
	if _, err = store.db.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	q := durableFixtureQueue(t, store, c)
	if err = q.scanUpload(t.Context(), c, &task); err == nil {
		t.Fatal("scan did not cross the 128-entry boundary")
	}
	var count int
	if err = store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 129 {
		t.Fatalf("discovery transaction not bounded/atomic: %d", count)
	}
	if _, err = store.db.ExecContext(t.Context(), `DROP TRIGGER interrupt_scan`); err != nil {
		t.Fatal(err)
	}
	var completed int
	if err = store.db.QueryRowContext(t.Context(), `SELECT id FROM transfer_tasks WHERE batch_id=? AND kind='upload' AND state='queued' ORDER BY id LIMIT 1`, batch).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(t.Context(), `UPDATE transfer_tasks SET state='done' WHERE id=?`, completed); err != nil {
		t.Fatal(err)
	}
	store.db.Close()
	store, err = openDurable(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	task, err = store.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != "scan_upload" {
		t.Fatalf("interrupted scanner not recovered: %+v", task)
	}
	q = durableFixtureQueue(t, store, c)
	if err = q.scanUpload(t.Context(), c, &task); err != nil {
		t.Fatal(err)
	}
	if err = store.finish(t.Context(), task, "done", "", 0); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&count); err != nil {
		t.Fatal(err)
	}
	want := 262
	if linked {
		want++
	}
	if count != want {
		t.Fatalf("scan lost or duplicated entries: got %d want %d", count, want)
	}
	var state string
	if err = store.db.QueryRowContext(t.Context(), `SELECT state FROM transfer_tasks WHERE id=?`, completed).Scan(&state); err != nil || state != "done" {
		t.Fatalf("completed child was reset: %s %v", state, err)
	}
	if linked {
		if err = store.db.QueryRowContext(t.Context(), `SELECT state FROM transfer_tasks WHERE batch_id=? AND name='link'`, batch).Scan(&state); err != nil || state != "skipped" {
			t.Fatalf("symlink not reported: %s %v", state, err)
		}
	}
	empty, err := readTask(store.db.QueryRowContext(t.Context(), `SELECT `+taskColumns+` FROM transfer_tasks t WHERE batch_id=? AND kind='scan_upload' AND name='empty'`, batch))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(t.Context(), `UPDATE transfer_tasks SET state='active' WHERE id=?`, empty.ID); err != nil {
		t.Fatal(err)
	}
	q.run(t.Context(), c, empty)
	dirMu.Lock()
	defer dirMu.Unlock()
	if len(dirs) != 2 {
		t.Fatalf("empty directory not preserved or root duplicated: %v", dirs)
	}
}

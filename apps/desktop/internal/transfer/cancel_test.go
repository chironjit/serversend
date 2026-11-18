package transfer

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func id() protocol.ID {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return protocol.ID(hex.EncodeToString(value))
}

// fakeServer implements just enough of the transfer API for worker tests.
type fakeServer struct {
	mu             sync.Mutex
	batches        [][]string // file names per batch request
	singles        int
	cancelled      []string
	blockChunks    bool
	chunkStarted   chan struct{}
	createdFolders int
}

func (f *fakeServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, api.BasePath)
		reply := func(status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		}
		committed := func(m protocol.Manifest) api.FinalizeResult {
			return api.FinalizeResult{UploadID: id(), FileID: m.FileID, VersionID: m.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case path == "/catalog/target":
			reply(200, struct {
				Entry *api.CatalogEntry `json:"entry"`
			}{nil})
		case path == "/sessions":
			reply(201, api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "memory-session"})
		case path == "/capabilities":
			reply(200, api.Capabilities{ProtocolVersions: []protocol.Version{protocol.Version1}, Transports: []api.Transport{api.HTTP2}, Features: []string{api.FeatureBatchUpload},
				Limits: protocol.TransferLimits{MinChunkBytes: 1 << 20, MaxChunkBytes: 8 << 20, MaxRequestBytes: 8 << 20, MaxManifestBytes: 1 << 20, MaxChunks: 1 << 20}})
		case path == "/directories":
			f.createdFolders++
			reply(201, api.CatalogEntry{ID: id(), Kind: api.Directory, Mode: protocol.Standard, HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: "folder"}})
		case path == api.BatchUploadPath:
			n, _ := strconv.Atoi(r.Header.Get(api.BatchHeaderBytesHeader))
			var request api.BatchUploadRequest
			_ = json.NewDecoder(io.LimitReader(r.Body, int64(n))).Decode(&request)
			names, response := []string{}, api.BatchUploadResponse{}
			for _, file := range request.Files {
				var m protocol.Manifest
				_ = json.Unmarshal(file.Manifest, &m)
				names = append(names, m.Standard.Name)
				result := committed(m)
				response.Results = append(response.Results, api.BatchUploadResult{Result: &result})
			}
			f.batches = append(f.batches, names)
			reply(200, response)
		case path == "/uploads" && r.Method == "POST":
			f.singles++
			var request api.CreateUploadRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			reply(201, api.Upload{ID: id(), CreateUploadRequest: request, State: protocol.UploadOpen, ExpiresAt: time.Now().Add(time.Hour)})
		case strings.Contains(path, "/chunks/"):
			if f.blockChunks {
				close(f.chunkStarted)
				f.mu.Unlock()
				<-r.Context().Done()
				f.mu.Lock()
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			reply(201, struct{}{})
		case strings.HasSuffix(path, "/finalize"):
			var m protocol.Manifest
			_ = json.NewDecoder(r.Body).Decode(&m)
			result := committed(m)
			result.UploadID = protocol.ID(strings.TrimSuffix(strings.TrimPrefix(path, "/uploads/"), "/finalize"))
			reply(200, result)
		case r.Method == "DELETE" && strings.HasPrefix(path, "/uploads/"):
			f.cancelled = append(f.cancelled, strings.TrimPrefix(path, "/uploads/"))
			reply(200, struct{}{})
		default:
			t.Errorf("unexpected %s %s", r.Method, path)
			w.WriteHeader(500)
		}
	}
}

func startFake(t *testing.T, queue int) (*Manager, *fakeServer) {
	t.Helper()
	fake := &fakeServer{chunkStarted: make(chan struct{})}
	server := httptest.NewUnstartedServer(fake.handler(t))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	digest := sha256.Sum256(server.Certificate().Raw)
	c, err := client.New(server.URL, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if _, err := c.SignIn(t.Context(), "challenge", "123456", "test-device"); err != nil {
		t.Fatal(err)
	}
	return &Manager{ctx: t.Context(), client: c, queue: make(chan job, queue), emit: func(Item) {}}, fake
}

func writeFiles(t *testing.T, sizes map[string]int) (string, []string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for name, size := range sizes {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return dir, paths
}

func TestWorkerBatchesSmallFilesAndSendsLargeOnesAlone(t *testing.T) {
	m, fake := startFake(t, 10)
	_, paths := writeFiles(t, map[string]int{"a": 10, "b": 0, "c": 1 << 20, "large": client.MaxBatchFileBytes + 1})
	if err := m.Upload(paths, ""); err != nil {
		t.Fatal(err)
	}
	for len(m.queue) > 0 {
		m.dispatch(<-m.queue)
	}
	b := m.Batches()[0]
	if b.State != "done" || b.Completed != 4 || b.Bytes != 10+(1<<20)+client.MaxBatchFileBytes+1 {
		t.Fatalf("batch: %+v", b)
	}
	batched := 0
	for _, names := range fake.batches {
		batched += len(names)
	}
	if batched != 3 || fake.singles != 1 || len(fake.batches) > 2 {
		t.Fatalf("batches %v, single uploads %d", fake.batches, fake.singles)
	}
}

func TestCancelTransferInterruptsUploadAndReleasesServerUpload(t *testing.T) {
	m, fake := startFake(t, 10)
	fake.blockChunks = true
	_, paths := writeFiles(t, map[string]int{"large": client.MaxBatchFileBytes + 1})
	if err := m.Upload(paths, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.dispatch(<-m.queue); close(done) }()
	<-fake.chunkStarted
	m.CancelTransfer(1)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not interrupt the upload")
	}
	if item := m.Transfers()[0]; item.State != "cancelled" {
		t.Fatalf("item: %+v", item)
	}
	if b := m.Batches()[0]; b.State != "cancelled" || b.Cancelled != 1 || b.Failed != 0 {
		t.Fatalf("batch: %+v", b)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.cancelled) != 1 {
		t.Fatalf("server upload not released: %v", fake.cancelled)
	}
}

func TestCancelBatchStopsScanAndSkipsQueuedFiles(t *testing.T) {
	m, fake := startFake(t, 2)
	dir, _ := writeFiles(t, map[string]int{"1": 1, "2": 1, "3": 1, "4": 1, "5": 1})
	if err := m.StartFolderUpload(dir, ""); err != nil {
		t.Fatal(err)
	}
	// The scan blocks on the full queue until cancelled.
	for deadline := time.Now().Add(10 * time.Second); len(m.queue) < 2; {
		if time.Now().After(deadline) {
			t.Fatal("scan did not fill the queue")
		}
		time.Sleep(time.Millisecond)
	}
	m.CancelBatch(1)
	for deadline := time.Now().Add(10 * time.Second); m.Batches()[0].State == "scanning"; {
		if time.Now().After(deadline) {
			t.Fatal("scan did not stop")
		}
		time.Sleep(time.Millisecond)
	}
	for len(m.queue) > 0 {
		m.dispatch(<-m.queue)
	}
	b := m.Batches()[0]
	if b.State != "cancelled" || b.Cancelled != 3 || b.Completed != 0 || b.Error != "" || b.Files != 3 {
		t.Fatalf("batch: %+v", b)
	}
	if len(fake.batches) != 0 || fake.singles != 0 {
		t.Fatal("cancelled files reached the server")
	}
}

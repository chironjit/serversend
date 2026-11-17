package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

var durableRestoreScope = protocol.Scope{ServerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AccountID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}

func durableRestoreClient(t *testing.T, handler http.Handler) *client.Client {
	t.Helper()
	session := api.Session{ServerID: durableRestoreScope.ServerID, AccountID: durableRestoreScope.AccountID, Role: "user", AccessToken: "restore-test-session"}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case api.BasePath + "/sessions/current":
			_ = json.NewEncoder(w).Encode(session)
		case api.BasePath + "/capabilities":
			_ = json.NewEncoder(w).Encode(api.Capabilities{ProtocolVersions: []protocol.Version{protocol.Version1}, Transports: []api.Transport{api.HTTP2}, Limits: protocol.TransferLimits{MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536, MaxManifestBytes: 65536, MaxChunks: 100}})
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	digest := sha256.Sum256(server.Certificate().Raw)
	c, err := client.New(server.URL, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if err := c.RestoreSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	return c
}
func durableRestoreQueue(t *testing.T, dir string) (*durableQueue, *durableStore) {
	t.Helper()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := openDurable(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{ctx: t.Context(), emit: func(Item) {}}
	q := &durableQueue{manager: m, store: store, wake: make(chan struct{}, workers), active: map[int]activeDurable{}}
	m.durable = q
	return q, store
}
func durableRestoreDrain(t *testing.T, q *durableQueue, c *client.Client) {
	t.Helper()
	for range 1000 {
		task, err := q.store.claim(t.Context(), durableRestoreScope)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		q.run(t.Context(), c, task)
	}
	t.Fatal("restore did not settle within bounded fixture tasks")
}
func durableRestoreManifest(entry api.CatalogEntry, data []byte) protocol.Manifest {
	m := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: durableRestoreScope, FileID: entry.ID, VersionID: entry.HeadVersionID, Mode: protocol.Standard, Standard: entry.Standard, Chunks: []protocol.Chunk{}, TotalBytes: int64(len(data))}
	for offset := 0; offset < len(data); offset += 4096 {
		chunk := data[offset:min(offset+4096, len(data))]
		digest := sha256.Sum256(chunk)
		m.Chunks = append(m.Chunks, protocol.Chunk{Index: int64(len(m.Chunks)), Bytes: int64(len(chunk)), SHA256: protocol.Digest(hex.EncodeToString(digest[:]))})
	}
	return m
}

func TestDurableRestorePagesAndCompletedRestart(t *testing.T) {
	folder := restoreDirectoryEntry("11111111111111111111111111111111", "", "folder")
	empty := restoreDirectoryEntry("22222222222222222222222222222222", folder.ID, "empty")
	files := make([]api.CatalogEntry, 101)
	byID := map[string]api.CatalogEntry{}
	for i := range files {
		files[i] = api.CatalogEntry{ID: protocol.ID(fmt.Sprintf("%032x", i+100)), ParentID: folder.ID, Kind: api.File, Mode: protocol.Standard, HeadVersionID: protocol.ID(fmt.Sprintf("%032x", i+1000)), HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: fmt.Sprintf("file-%03d", i)}}
		byID[string(files[i].ID)] = files[i]
	}
	var mu sync.Mutex
	manifests := map[string]int{}
	c := durableRestoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == api.BasePath+"/catalog" {
			entries := []api.CatalogEntry{}
			next := api.Cursor("")
			if r.URL.Query().Get("parent_id") == string(folder.ID) {
				if r.URL.Query().Get("cursor") == "" {
					entries = files[:100]
					next = "YQ"
				} else {
					entries = append([]api.CatalogEntry{files[100]}, empty)
				}
			}
			_ = json.NewEncoder(w).Encode(api.CatalogPage{Page: api.Page{Snapshot: "55555555555555555555555555555555", NextCursor: next}, CatalogRevision: "9", Entries: entries})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			id := strings.Split(strings.TrimPrefix(r.URL.Path, api.BasePath+"/files/"), "/")[0]
			mu.Lock()
			manifests[id]++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(durableRestoreManifest(byID[id], nil))
			return
		}
		t.Errorf("unexpected restore request: %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	q, store := durableRestoreQueue(t, config)
	batch, err := store.create(t.Context(), durableRestoreScope, "folder", []durableTask{{Kind: "scan_restore", Name: "folder", Payload: durablePayload{Path: filepath.Join(root, "folder"), Root: root, Entry: folder, Policy: "fail"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Discover both pages and finish some children, then simulate app restart.
	for range 12 {
		task, err := store.claim(t.Context(), durableRestoreScope)
		if err != nil {
			t.Fatal(err)
		}
		q.run(t.Context(), c, task)
	}
	var completedBefore int
	if err := store.db.QueryRow(`SELECT count(*) FROM transfer_tasks WHERE batch_id=? AND state='done'`, batch).Scan(&completedBefore); err != nil {
		t.Fatal(err)
	}
	if completedBefore < 10 {
		t.Fatalf("insufficient completed restart fixture: %d", completedBefore)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	q, store = durableRestoreQueue(t, config)
	defer store.db.Close()
	durableRestoreDrain(t, q, c)
	var total, done int
	if err := store.db.QueryRow(`SELECT count(*),sum(state='done') FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&total, &done); err != nil {
		t.Fatal(err)
	}
	if total != 103 || done != total {
		t.Fatalf("restore task outcomes: %d/%d", done, total)
	}
	for _, entry := range files {
		info, err := os.Stat(filepath.Join(root, "folder", entry.Standard.Name))
		if err != nil || info.Size() != 0 {
			t.Fatalf("restored file %s: %v", entry.Standard.Name, err)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "folder", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty folder missing: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for id, n := range manifests {
		if n != 1 {
			t.Fatalf("completed file replayed after restart: %s calls=%d", id, n)
		}
	}
}

func TestDurableInterruptedDownloadResumesAfterRestart(t *testing.T) {
	entry := api.CatalogEntry{ID: "33333333333333333333333333333333", Kind: api.File, Mode: protocol.Standard, HeadVersionID: "44444444444444444444444444444444", HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: "file"}}
	data := append(bytes.Repeat([]byte("a"), 4096), bytes.Repeat([]byte("b"), 4096)...)
	manifest := durableRestoreManifest(entry, data)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var interrupted atomic.Bool
	var first, second atomic.Int64
	c := durableRestoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest"):
			_ = json.NewEncoder(w).Encode(manifest)
		case strings.HasSuffix(r.URL.Path, "/chunks/0"):
			first.Add(1)
			_, _ = w.Write(data[:4096])
		case strings.HasSuffix(r.URL.Path, "/chunks/1"):
			second.Add(1)
			if interrupted.CompareAndSwap(false, true) {
				cancel()
				return
			}
			_, _ = w.Write(data[4096:])
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "restored")
	config := t.TempDir()
	q, store := durableRestoreQueue(t, config)
	batch, err := store.create(t.Context(), durableRestoreScope, "restore", []durableTask{{Kind: "download", Name: "file", Payload: durablePayload{Path: destination, Root: root, Entry: entry, Policy: "fail"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.claim(t.Context(), durableRestoreScope)
	if err != nil {
		t.Fatal(err)
	}
	q.run(ctx, c, task)
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("interrupted download published")
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	q, store = durableRestoreQueue(t, config)
	defer store.db.Close()
	durableRestoreDrain(t, q, c)
	var state string
	if err := store.db.QueryRow(`SELECT state FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&state); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(destination)
	if state != "done" || !bytes.Equal(got, data) || first.Load() != 1 || second.Load() != 2 {
		t.Fatalf("durable resume: state=%s chunks=%d/%d bytes=%d", state, first.Load(), second.Load(), len(got))
	}
}

func TestDurableRestoreReconcilesPublicationBeforeTaskAcknowledgement(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("published before process exit")} {
		t.Run(fmt.Sprintf("bytes-%d", len(data)), func(t *testing.T) {
			entry := api.CatalogEntry{ID: "33333333333333333333333333333333", Kind: api.File, Mode: protocol.Standard, HeadVersionID: "44444444444444444444444444444444", HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: "file"}}
			manifest := durableRestoreManifest(entry, data)
			c := durableRestoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/manifest") {
					t.Errorf("published file transferred again: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(manifest)
			}))
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "restored")
			config := t.TempDir()
			_, store := durableRestoreQueue(t, config)
			batch, err := store.create(t.Context(), durableRestoreScope, "restore", []durableTask{{Kind: "download", Name: "file", Payload: durablePayload{Path: destination, Root: root, Entry: entry, Policy: "fail"}}})
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.claim(t.Context(), durableRestoreScope)
			if err != nil {
				t.Fatal(err)
			}
			task.Done, task.Total = int64(len(data)), int64(len(data))
			task.Payload.DownloadReady = true
			if err := store.save(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			// Reproduce the exact crash boundary: the file is fully published but the
			// durable task is still active, with no terminal completion acknowledgement.
			if err := os.WriteFile(destination, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := store.db.Close(); err != nil {
				t.Fatal(err)
			}
			q, store := durableRestoreQueue(t, config)
			defer store.db.Close()
			durableRestoreDrain(t, q, c)
			var state string
			if err := store.db.QueryRow(`SELECT state FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "done" {
				t.Fatalf("published fail-policy file became %s", state)
			}
			got, _ := os.ReadFile(destination)
			if !bytes.Equal(got, data) {
				t.Fatal("published destination changed")
			}
		})
	}
}

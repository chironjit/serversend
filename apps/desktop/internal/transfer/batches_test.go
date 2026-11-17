package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
)

func TestDirectSelectionReportsBoundedSkipsAndQueuesOrdinaryHardLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(root, "ordinary")
	if err := os.WriteFile(ordinary, []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(root, "hard")
	if err := os.Link(ordinary, hard); err != nil {
		t.Skipf("hard link fixture unavailable: %v", err)
	}
	m := &Manager{client: &client.Client{}, queue: make(chan job, 10), emit: func(Item) {}}
	paths := []string{}
	for i := range 125 {
		path := filepath.Join(root, fmt.Sprintf("link-%03d", i))
		if err := os.Symlink(ordinary, path); err != nil {
			t.Skipf("native symlink fixture unavailable: %v", err)
		}
		paths = append(paths, path)
	}
	if err := m.Upload(paths, ""); err != nil {
		t.Fatal(err)
	}
	b := m.Batches()[0]
	if b.State != "all_skipped" || b.Skipped != 125 || len(b.SkippedItems) != 100 || len(m.queue) != 0 {
		t.Fatalf("skip report: %+v queued=%d", b, len(m.queue))
	}
	if err := m.Upload(append(paths, ordinary, hard), ""); err != nil {
		t.Fatal(err)
	}
	b = m.Batches()[1]
	if b.Files != 2 || b.Skipped != 125 || b.State != "running" || len(m.queue) != 2 {
		t.Fatalf("mixed report: %+v", b)
	}
	for range 2 {
		j := <-m.queue
		m.finishFile(j.item.BatchID, nil)
	}
	b = m.Batches()[1]
	if b.State != "done_with_skips" || b.Completed != 2 {
		t.Fatalf("completion report: %+v", b)
	}
}

func TestWorkerRechecksSourceAndReportsLinkReplacementAsSkipped(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.BasePath+"/sessions" {
			t.Errorf("replacement link reached network: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "memory-session"})
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	digest := sha256.Sum256(server.Certificate().Raw)
	c, err := client.New(server.URL, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.SignIn(t.Context(), "challenge", "123456", "test-device"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "eligible")
	target := filepath.Join(root, "outside")
	for _, name := range []string{path, target} {
		if err := os.WriteFile(name, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{client: c, queue: make(chan job, 10), emit: func(Item) {}}
	if err := m.Upload([]string{path}, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("native symlink fixture unavailable: %v", err)
	}
	m.run(t.Context(), <-m.queue)
	b := m.Batches()[0]
	if b.State != "all_skipped" || b.Completed != 0 || b.Skipped != 1 || b.Failed != 0 {
		t.Fatalf("replacement report: %+v", b)
	}
	item := m.Transfers()[0]
	if item.State != "skipped" || item.Error == "" {
		t.Fatalf("replacement item: %+v", item)
	}
}

func TestScanFailuresAndCancellationAreNotSuccessfulSkips(t *testing.T) {
	// macOS temporary paths can contain directory aliases. Resolve the
	// existing fixture root so this tests a missing file, not a policy skip.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{client: &client.Client{}, queue: make(chan job, 10), emit: func(Item) {}}
	if err := m.Upload([]string{filepath.Join(root, "missing")}, ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source: %v", err)
	}
	b := m.Batches()[0]
	if b.State != "failed" || b.Error == "" || b.Skipped != 0 {
		t.Fatalf("failure report: %+v", b)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.UploadFolder(ctx, root, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	b = m.Batches()[1]
	if b.State != "failed" || b.Skipped != 0 || b.Error == "" {
		t.Fatalf("cancellation report: %+v", b)
	}
}

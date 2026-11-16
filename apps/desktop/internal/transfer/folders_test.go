package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderScanPreservesHierarchyAndEmptyFolders(t *testing.T) {
	var requests []api.CreateDirectoryRequest
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.BasePath+"/directories" {
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var request api.CreateDirectoryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		_ = json.NewEncoder(w).Encode(api.CatalogEntry{ID: request.ID, ParentID: request.ParentID, Mode: protocol.Standard, Kind: api.Directory, Standard: request.Standard, HeadRevision: protocol.Revision(fmt.Sprint(len(requests)))})
	}))
	defer server.Close()
	digest := sha256.Sum256(server.Certificate().Raw)
	c, err := client.New(server.URL, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m := &Manager{client: c, queue: make(chan job, 10), emit: func(Item) {}}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(root, "sub", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"first.txt", "sub/second.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	destination := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := m.UploadFolder(t.Context(), root, destination); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[0].ParentID != protocol.ID(destination) || requests[1].ParentID != requests[0].ID || requests[2].ParentID != requests[1].ID {
		t.Fatalf("hierarchy: %+v", requests)
	}
	if len(m.queue) != 2 {
		t.Fatalf("queued %d", len(m.queue))
	}
	for range 2 {
		j := <-m.queue
		expected := requests[0].ID
		if filepath.Base(j.path) == "second.txt" {
			expected = requests[1].ID
		}
		if j.parent != expected || j.client != c {
			t.Fatalf("wrong destination %+v", j)
		}
	}
	if err := m.SignOut(t.Context()); err == nil {
		t.Fatal("changed session with queued jobs")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := m.UploadFolder(t.Context(), link, destination); err == nil {
		t.Fatal("followed symbolic link")
	}
}

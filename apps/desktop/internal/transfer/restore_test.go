package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

func restoreDirectoryEntry(id, parent protocol.ID, name string) api.CatalogEntry {
	return api.CatalogEntry{ID: id, ParentID: parent, Kind: api.Directory, Mode: protocol.Standard, HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: name}}
}

func TestRestorePlannerKeepsVersionsAndPages(t *testing.T) {
	folder := restoreDirectoryEntry("11111111111111111111111111111111", "", "folder")
	child := restoreDirectoryEntry("22222222222222222222222222222222", folder.ID, "empty")
	file := api.CatalogEntry{ID: "33333333333333333333333333333333", ParentID: folder.ID, Kind: api.File, Mode: protocol.Standard, HeadRevision: "1", HeadVersionID: "44444444444444444444444444444444", Standard: &protocol.StandardMetadata{Name: "file"}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parent_id") != string(folder.ID) || r.URL.Query().Get("limit") != "100" {
			t.Errorf("wrong bounded query: %s", r.URL.RawQuery)
		}
		entries := []api.CatalogEntry{child}
		next := api.Cursor("YQ")
		if r.URL.Query().Get("cursor") == "YQ" {
			entries = []api.CatalogEntry{file}
			next = ""
		}
		_ = json.NewEncoder(w).Encode(api.CatalogPage{Page: api.Page{Snapshot: "55555555555555555555555555555555", NextCursor: next}, CatalogRevision: "7", Entries: entries})
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
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "folder")
	first, err := PlanRestoreDirectory(t.Context(), c, folder, target, "", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 1 || first.Tasks[0].Kind != "scan_restore" || first.NextCursor != "YQ" || first.Revision != "7" {
		t.Fatalf("first page: %+v", first)
	}
	second, err := PlanRestoreDirectory(t.Context(), c, folder, target, first.NextCursor, "fail")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].Kind != "download" || second.Tasks[0].Entry.HeadVersionID != file.HeadVersionID || second.NextCursor != "" {
		t.Fatalf("second page: %+v", second)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("directory missing: %v", err)
	}
}

func TestRestorePlannerRejectsRemoteNameCollisions(t *testing.T) {
	for _, names := range [][]string{{"File", "file"}, {"é", "e\u0301"}, {"CON.txt"}, {"has:colon"}} {
		t.Run(names[0], func(t *testing.T) {
			folder := restoreDirectoryEntry("11111111111111111111111111111111", "", "folder")
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entries := []api.CatalogEntry{}
				for i, name := range names {
					id := protocol.ID("22222222222222222222222222222222")
					if i == 1 {
						id = "33333333333333333333333333333333"
					}
					entries = append(entries, restoreDirectoryEntry(id, folder.ID, name))
				}
				_ = json.NewEncoder(w).Encode(api.CatalogPage{Page: api.Page{Snapshot: "55555555555555555555555555555555"}, CatalogRevision: "1", Entries: entries})
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
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := PlanRestoreDirectory(t.Context(), c, folder, filepath.Join(root, "folder"), "", "replace"); err == nil {
				t.Fatal("unsafe or colliding names accepted")
			}
		})
	}
}

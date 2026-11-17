package uploads

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func seedCatalog(t *testing.T, s *Service, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := s.repo.CreateDirectory(t.Context(), owner, api.CreateDirectoryRequest{ID: protocol.ID(fmt.Sprintf("%032x", i+1)), Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: fmt.Sprintf("folder-%04d", i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogPagesReachBeyondOneThousandAtOneRevision(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	seedCatalog(t, s, 1205)
	for _, limit := range []int{100, 1000} {
		request := api.PageRequest{Limit: limit}
		seen := map[protocol.ID]bool{}
		var snapshot protocol.ID
		var revision protocol.Revision
		pages := 0
		for {
			page, err := s.CatalogPage(t.Context(), owner, "", request)
			if err != nil {
				t.Fatal(err)
			}
			if err := page.Validate(nil); err != nil {
				t.Fatal(err)
			}
			if pages == 0 {
				snapshot, revision = page.Snapshot, page.CatalogRevision
			}
			if page.Snapshot != snapshot || page.CatalogRevision != revision || len(page.Entries) > limit {
				t.Fatalf("page mixed snapshots: %+v", page)
			}
			for _, entry := range page.Entries {
				if seen[entry.ID] {
					t.Fatal("duplicate entry")
				}
				seen[entry.ID] = true
			}
			pages++
			if page.NextCursor == "" {
				break
			}
			if len(page.Entries) == 0 {
				t.Fatal("empty continuation")
			}
			request.Cursor = page.NextCursor
		}
		if len(seen) != 1205 {
			t.Fatalf("limit=%d reached=%d", limit, len(seen))
		}
	}
}

func TestCatalogCursorScopeTamperExpiryAndRestart(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	seedCatalog(t, s, 3)
	now := time.Now()
	s.now = func() time.Time { return now }
	first, err := s.CatalogPage(t.Context(), owner, "", api.PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := api.PageRequest{Limit: 1, Cursor: first.NextCursor}
	if _, err := s.CatalogPage(t.Context(), serverID, "", request); err == nil {
		t.Fatal("foreign account continuation accepted")
	} else {
		code(t, err, api.InvalidRequest)
	}
	if _, err := s.CatalogPage(t.Context(), owner, serverID, request); err == nil {
		t.Fatal("foreign folder continuation accepted")
	} else {
		code(t, err, api.InvalidRequest)
	}
	changed := request
	changed.Limit = 2
	if _, err := s.CatalogPage(t.Context(), owner, "", changed); err == nil {
		t.Fatal("changed page size accepted")
	} else {
		code(t, err, api.InvalidRequest)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(string(request.Cursor))
	raw[len(raw)-1] ^= 1
	changed = request
	changed.Cursor = api.Cursor(base64.RawURLEncoding.EncodeToString(raw))
	if _, err := s.CatalogPage(t.Context(), owner, "", changed); err == nil {
		t.Fatal("tampered continuation accepted")
	} else {
		code(t, err, api.InvalidRequest)
	}
	s.now = func() time.Time { return now.Add(catalogSnapshotLifetime) }
	if _, err := s.CatalogPage(t.Context(), owner, "", request); err == nil {
		t.Fatal("expired continuation accepted")
	} else {
		code(t, err, api.SnapshotExpired)
	}
	restarted, err := New(s.repo, s.store, serverID, s.limits, nil, StoragePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CatalogPage(t.Context(), owner, "", request); err == nil {
		t.Fatal("old instance continuation accepted")
	} else {
		code(t, err, api.SnapshotExpired)
	}
}

func TestCatalogMutationExpiresContinuationAndHistoryRequiresRefresh(t *testing.T) {
	s, db, _ := fixture(t, StoragePolicy{})
	seedCatalog(t, s, 3)
	first, err := s.CatalogPage(t.Context(), owner, "", api.PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.CatalogChanges(t.Context(), owner, first.CatalogRevision)
	if err != nil || unchanged.Validate(nil) != nil || len(unchanged.Changes) != 0 {
		t.Fatalf("unchanged revision: %+v %v", unchanged, err)
	}
	// Insert sorts before the first page's cursor. An offset/keyset-only listing
	// would silently lose it; the revision guard must reject continuation.
	_, err = database.NewRepository(db).CreateDirectory(t.Context(), owner, api.CreateDirectoryRequest{ID: "00000000000000000000000000000000", Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "added-before-cursor"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CatalogPage(t.Context(), owner, "", api.PageRequest{Limit: 1, Cursor: first.NextCursor}); err == nil {
		t.Fatal("mixed revision accepted")
	} else {
		code(t, err, api.SnapshotExpired)
	}
	if _, err = s.CatalogChanges(t.Context(), owner, first.CatalogRevision); err == nil {
		t.Fatal("stale history accepted")
	} else {
		code(t, err, api.SnapshotExpired)
	}
	fresh, err := s.CatalogPage(t.Context(), owner, "", api.PageRequest{Limit: 100})
	if err != nil || len(fresh.Entries) != 4 {
		t.Fatalf("fresh snapshot: %d %v", len(fresh.Entries), err)
	}
}

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestCatalogPaginationAndRevisionReconciliationOverTLS(t *testing.T) {
	config := fixture(t)
	s, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); <-done })
	origin := "https://" + listener.Addr().String()
	probe, err := client.Probe(t.Context(), origin)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := onboardedClient(t, origin, probe.Fingerprint, "admin", issued.TemporaryPassword)
	user, err := admin.CreateUser(t.Context(), "catalog-user")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := onboardedClient(t, origin, probe.Fingerprint, "catalog-user", user.TemporaryPassword)
	account, _ := c.Account()
	// Seed through the real service and SQLite/filesystem journal; only browsing
	// uses the network, so this test does not depend on 1005 frontend actions.
	for i := 0; i < 1005; i++ {
		_, err := s.transfers.CreateDirectory(t.Context(), account.AccountID, api.CreateDirectoryRequest{ID: protocol.ID(fmt.Sprintf("%032x", i+1)), Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: fmt.Sprintf("folder-%04d", i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	cursor := api.Cursor("")
	seen := map[protocol.ID]bool{}
	var revision protocol.Revision
	var first api.CatalogPage
	for {
		page, err := c.CatalogFolderPage(t.Context(), "", cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) == 0 {
			first = page
			revision = page.CatalogRevision
		}
		if len(page.Entries) > api.DefaultPageSize || page.CatalogRevision != revision || page.Snapshot != first.Snapshot {
			t.Fatal("unbounded or mixed catalog page")
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatal("duplicate entry")
			}
			seen[entry.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 1005 {
		t.Fatalf("reachable=%d", len(seen))
	}
	if changes, err := c.CatalogChanges(t.Context(), revision); err != nil || len(changes.Changes) != 0 {
		t.Fatalf("unchanged reconciliation: %+v %v", changes, err)
	}
	if _, err := c.CreateDirectory(t.Context(), "", "after-listing"); err != nil {
		t.Fatal(err)
	}
	var failure api.Error
	if _, err := c.CatalogFolderPage(t.Context(), "", first.NextCursor); !errors.As(err, &failure) || failure.Code != api.SnapshotExpired {
		t.Fatalf("mixed revision continuation: %v", err)
	}
	if _, err := c.CatalogChanges(t.Context(), revision); !errors.As(err, &failure) || failure.Code != api.SnapshotExpired {
		t.Fatalf("stale reconciliation: %v", err)
	}
	refreshed, err := c.CatalogFolderPage(t.Context(), "", "")
	if err != nil || refreshed.CatalogRevision == revision {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
}

package uploads

import (
	"bytes"
	"context"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"testing"
	"time"
)

func TestStorageVisibilityRecoveryRetryAndCancel(t *testing.T) {
	s, db, _ := fixture(t, StoragePolicy{UserBytes: 100})
	ctx := context.Background()
	u := create(t, s, 9)
	stats, err := s.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ReservedBytes != 9 || stats.UnwrittenBytes != 9 || stats.ActiveUploads != 1 || stats.AvailableBytes != 91 {
		t.Fatalf("reservation: %+v", stats)
	}
	b := []byte("123456789")
	for range 2 {
		if _, _, err := s.PutChunk(ctx, owner, u.ID, 0, 9, digest(b), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	stats, err = s.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.StagedBytes < 9 || stats.UnwrittenBytes != 0 || stats.Transfer.WireBytes != 18 || stats.Transfer.UsefulBytes != 9 || stats.Transfer.RetryBytes != 9 {
		t.Fatalf("staged/retry: %+v", stats)
	}
	recovered, err := New(database.NewRepository(db), s.store, serverID, s.limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := recovered.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if after.StagedBytes != stats.StagedBytes || after.ReservedBytes != 9 {
		t.Fatalf("restart: %+v", after)
	}
	if err := s.Cancel(ctx, owner, u.ID); err != nil {
		t.Fatal(err)
	}
	stats, err = s.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.StagedBytes != 0 || stats.ReservedBytes != 0 || stats.ActiveUploads != 0 {
		t.Fatalf("cancel: %+v", stats)
	}
}

func TestTrafficWindowAndIsolation(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	now := time.Now()
	s.now = func() time.Time { return now }
	other := protocol.ID("cccccccccccccccccccccccccccccccc")
	s.RecordTraffic(owner, 20, 10, 10)
	s.RecordTraffic(other, 50, 50, 0)
	if got := s.trafficStats(owner); got.WireBytes != 20 || got.WireRate != 2 {
		t.Fatalf("scope: %+v", got)
	}
	if got := s.trafficStats(""); got.WireBytes != 70 {
		t.Fatalf("global: %+v", got)
	}
	now = now.Add(11 * time.Second)
	if got := s.trafficStats(owner); got.WireRate != 0 {
		t.Fatalf("idle: %+v", got)
	}
}

func TestCommittedRetainedDeletionAndAccountIsolation(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	ctx := context.Background()
	for range 2 {
		f := batchFile(t, "same.txt", []byte("hello"))
		outcomes, err := s.UploadBatch(ctx, owner, api.BatchUploadRequest{Files: []api.BatchUploadFile{f}}, bytes.NewReader([]byte("hello")))
		if err != nil || outcomes[0].Result == nil {
			t.Fatalf("publish: %v %+v", err, outcomes)
		}
	}
	stats, err := s.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LogicalBytes != 10 || stats.CommittedBytes < 5 || stats.TrashBytes < 5 || stats.StagedBytes != 0 || stats.ReservedBytes != 0 {
		t.Fatalf("published: %+v", stats)
	}
	global, err := s.measureStatistics(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if global.CommittedBytes != stats.CommittedBytes || global.RetainedBytes != stats.RetainedBytes {
		t.Fatalf("global differs: %+v %+v", global, stats)
	}
	page, err := s.CatalogPage(ctx, owner, "", api.PageRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEntry(ctx, owner, page.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	// A displaced file is retained independently until deleted explicitly.
	stats, err = s.measureStatistics(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CommittedBytes != 0 || stats.LogicalBytes != 10 || stats.TrashBytes == 0 {
		t.Fatalf("delete: %+v", stats)
	}
}

package uploads

import (
	"bytes"
	"encoding/json"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"io"
	"sync"
	"testing"
	"time"
)

func commitVersion(t *testing.T, s *Service, file, parent protocol.ID, head protocol.Revision, name, text string) api.FinalizeResult {
	t.Helper()
	b := []byte(text)
	entry := batchFile(t, name, b)
	entry.FileID = file
	entry.ParentID = parent
	entry.ExpectedHead = head
	var m protocol.Manifest
	if err := json.Unmarshal(entry.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.FileID = file
	entry.Manifest, _ = json.Marshal(m)
	entry.ManifestBytes = int64(len(entry.Manifest))
	u, _, err := s.Create(t.Context(), owner, string(newID()), entry.CreateUploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 0 {
		if _, _, err = s.PutChunk(t.Context(), owner, u.ID, 0, int64(len(b)), digest(b), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Finalize(t.Context(), owner, u.ID, entry.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func readVersion(t *testing.T, s *Service, file, version protocol.ID, want string) {
	t.Helper()
	r, _, err := s.OpenChunk(t.Context(), owner, file, version, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil || string(b) != want {
		t.Fatalf("bytes=%q err=%v want=%q", b, err, want)
	}
}
func TestVersionConflictResolutionAndRetention(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	file := newID()
	first := commitVersion(t, s, file, "", "0", "a", "one")
	second := commitVersion(t, s, file, "", first.HeadRevision, "a", "two")
	conflict := commitVersion(t, s, file, "", first.HeadRevision, "a", "three")
	if conflict.State != protocol.UploadConflicted {
		t.Fatal("lost concurrent edit")
	}
	page, err := s.History(t.Context(), owner, file, api.PageRequest{Limit: 1})
	if err != nil || len(page.Versions) != 1 || page.NextCursor == "" {
		t.Fatalf("history: %+v %v", page, err)
	}
	if _, err = s.History(t.Context(), serverID, file, api.PageRequest{Limit: 1, Cursor: page.NextCursor}); err == nil {
		t.Fatal("foreign history cursor")
	}
	if err = s.SelectVersion(t.Context(), owner, file, conflict.VersionID, first.HeadRevision); err == nil {
		t.Fatal("stale selection accepted")
	}
	readVersion(t, s, file, first.VersionID, "one")
	readVersion(t, s, file, second.VersionID, "two")
	readVersion(t, s, file, conflict.VersionID, "three")
	if err = s.SetRetention(t.Context(), api.RetentionPolicy{TrashDays: 30, VersionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24*time.Hour - time.Millisecond)
	if err = s.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	readVersion(t, s, file, first.VersionID, "one")
	now = now.Add(time.Millisecond)
	if err = s.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Manifest(t.Context(), owner, file, first.VersionID); err == nil {
		t.Fatal("expired version kept")
	}
	readVersion(t, s, file, second.VersionID, "two")
	readVersion(t, s, file, conflict.VersionID, "three")
	if err = s.SelectVersion(t.Context(), owner, file, conflict.VersionID, second.HeadRevision); err != nil {
		t.Fatal(err)
	}
	readVersion(t, s, file, conflict.VersionID, "three")
}
func TestTrashTreeRestorePurgeRestartAndReader(t *testing.T) {
	s, db, _ := fixture(t, StoragePolicy{})
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	dir := newID()
	if _, err := s.CreateDirectory(t.Context(), owner, api.CreateDirectoryRequest{ID: dir, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "folder"}}); err != nil {
		t.Fatal(err)
	}
	file := newID()
	v := commitVersion(t, s, file, dir, "0", "a", "content")
	if err := s.DeleteEntry(t.Context(), owner, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDirectory(t.Context(), owner, api.CreateDirectoryRequest{ID: newID(), ParentID: dir, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "bad"}}); err == nil {
		t.Fatal("write into trash")
	}
	page, err := s.TrashPage(t.Context(), owner, api.PageRequest{Limit: 100})
	if err != nil || len(page.Entries) != 1 || page.Validate(nil) != nil {
		t.Fatalf("trash %+v %v", page, err)
	}
	recovered, err := New(database.NewRepository(db), s.store, serverID, s.limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	recovered.now = s.now
	s = recovered
	readVersion(t, s, file, v.VersionID, "content")
	if err = s.RestoreTrash(t.Context(), serverID, dir); err == nil {
		t.Fatal("foreign restore")
	}
	if err = s.RestoreTrash(t.Context(), owner, dir); err != nil {
		t.Fatal(err)
	}
	readVersion(t, s, file, v.VersionID, "content")
	if err = s.PurgeTrash(t.Context(), owner, dir); err == nil {
		t.Fatal("purged active tree")
	}
	if err = s.DeleteEntry(t.Context(), owner, dir); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.OpenChunk(t.Context(), owner, file, v.VersionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeTrash(t.Context(), owner, dir); err == nil {
		t.Fatal("purged during active read")
	}
	b, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(b) != "content" {
		t.Fatal("purge interrupted reader", err)
	}
	if err = s.PurgeTrash(t.Context(), owner, dir); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = New(database.NewRepository(db), s.store, serverID, s.limits, nil); err != nil {
		t.Fatal("restart", err)
	}
}
func TestTrashRetentionBoundaryAndOccupiedRestore(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	file := newID()
	v := commitVersion(t, s, file, "", "0", "a", "old")
	if err := s.DeleteEntry(t.Context(), owner, file); err != nil {
		t.Fatal(err)
	}
	commitVersion(t, s, newID(), "", "0", "a", "new")
	if err := s.RestoreTrash(t.Context(), owner, file); err == nil {
		t.Fatal("overwrote occupied destination")
	}
	if err := s.SetRetention(t.Context(), api.RetentionPolicy{TrashDays: 1}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24*time.Hour - time.Millisecond)
	if err := s.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	readVersion(t, s, file, v.VersionID, "old")
	now = now.Add(time.Millisecond)
	if err := s.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Manifest(t.Context(), owner, file, v.VersionID); err == nil {
		t.Fatal("expired trash survived")
	}
	for _, p := range []api.RetentionPolicy{{TrashDays: 0}, {TrashDays: 1, VersionDays: -1}, {TrashDays: 36501}} {
		if s.SetRetention(t.Context(), p) == nil {
			t.Fatal("invalid policy")
		}
	}
}

func TestTwoDevicesFinalizeSameHeadConcurrently(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	file := newID()
	first := commitVersion(t, s, file, "", "0", "a", "base")
	results := make(chan api.FinalizeResult, 2)
	var wg sync.WaitGroup
	for _, text := range []string{"left", "right"} {
		wg.Go(func() { results <- commitVersion(t, s, file, "", first.HeadRevision, "a", text) })
	}
	wg.Wait()
	close(results)
	committed, conflicted := 0, 0
	for r := range results {
		if r.State == protocol.UploadCommitted {
			committed++
		} else if r.State == protocol.UploadConflicted {
			conflicted++
		}
		reader, _, err := s.OpenChunk(t.Context(), owner, file, r.VersionID, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || (string(b) != "left" && string(b) != "right") {
			t.Fatalf("lost edit %q %v", b, err)
		}
	}
	if committed != 1 || conflicted != 1 {
		t.Fatalf("committed=%d conflicted=%d", committed, conflicted)
	}
	page, err := s.History(t.Context(), owner, file, api.PageRequest{Limit: 100})
	if err != nil || len(page.Versions) != 3 {
		t.Fatalf("history %+v %v", page, err)
	}
}
func TestTrashJournalReplaysBeforeAndAfterFilesystemChanges(t *testing.T) {
	s, db, _ := fixture(t, StoragePolicy{})
	file := newID()
	v := commitVersion(t, s, file, "", "0", "a", "durable")
	restart := func() {
		t.Helper()
		var err error
		s, err = New(database.NewRepository(db), s.store, serverID, s.limits, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.repo.TrashEntry(t.Context(), owner, file, s.now()); err != nil {
		t.Fatal(err)
	}
	// Metadata is committed and the process exits before applying any moves.
	restart()
	restart()
	readVersion(t, s, file, v.VersionID, "durable")
	if err := s.repo.RestoreTrash(t.Context(), owner, file, s.now()); err != nil {
		t.Fatal(err)
	}
	restart()
	restart()
	readVersion(t, s, file, v.VersionID, "durable")
	if err := s.repo.TrashEntry(t.Context(), owner, file, s.now()); err != nil {
		t.Fatal(err)
	}
	restart()
	if err := s.repo.PurgeTrash(t.Context(), owner, file, s.now()); err != nil {
		t.Fatal(err)
	}
	// Run the journal without acknowledging it, then restart twice.
	ops, _, err := s.repo.Journal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.Apply(ops); err != nil {
		t.Fatal(err)
	}
	restart()
	restart()
	if _, err := s.Manifest(t.Context(), owner, file, v.VersionID); err == nil {
		t.Fatal("purged version survived")
	}
}

func TestPurgeRestoreFinalizeRacePreservesReferences(t *testing.T) {
	for range 8 {
		s, db, _ := fixture(t, StoragePolicy{})
		file := newID()
		old := commitVersion(t, s, file, "", "0", "a", "old")
		entry := batchFile(t, "a", []byte("new"))
		entry.FileID = file
		entry.ExpectedHead = old.HeadRevision
		var manifest protocol.Manifest
		if err := json.Unmarshal(entry.Manifest, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.FileID = file
		entry.Manifest, _ = json.Marshal(manifest)
		entry.ManifestBytes = int64(len(entry.Manifest))
		pending, _, err := s.Create(t.Context(), owner, string(newID()), entry.CreateUploadRequest)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.PutChunk(t.Context(), owner, pending.ID, 0, 3, digest([]byte("new")), bytes.NewReader([]byte("new"))); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteEntry(t.Context(), owner, file); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var restored, purged bool
		wg.Go(func() { restored = s.RestoreTrash(t.Context(), owner, file) == nil })
		wg.Go(func() { purged = s.PurgeTrash(t.Context(), owner, file) == nil })
		wg.Go(func() { _, _ = s.Finalize(t.Context(), owner, pending.ID, entry.Manifest) })
		wg.Wait()
		if restored == purged {
			t.Fatalf("restore=%t purge=%t", restored, purged)
		}
		if restored {
			readVersion(t, s, file, old.VersionID, "old")
		} else if _, err = s.Manifest(t.Context(), owner, file, old.VersionID); err == nil {
			t.Fatal("purged metadata survived")
		}
		if _, err = New(database.NewRepository(db), s.store, serverID, s.limits, nil); err != nil {
			t.Fatal("dangling reference after race", err)
		}
	}
}

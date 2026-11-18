package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

func TestBatchUploadEndToEnd(t *testing.T) {
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
	origin, pin := "https://"+listener.Addr().String(), ""
	probe, err := client.Probe(t.Context(), origin)
	if err != nil {
		t.Fatal(err)
	}
	pin = probe.Fingerprint
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := onboardedClient(t, origin, pin, "admin", issued.TemporaryPassword)
	user, err := admin.CreateUser(t.Context(), "batcher")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := onboardedClient(t, origin, pin, "batcher", user.TemporaryPassword)
	if !c.SupportsBatch(t.Context()) {
		t.Fatal("server does not advertise batch uploads")
	}
	folder, err := c.CreateDirectory(t.Context(), "", "Small files")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	fixtures := map[string][]byte{}
	files := []client.BatchFile{}
	for name, size := range map[string]int{"empty": 0, "one": 1, "note.txt": 1234, "limit.bin": client.MaxBatchFileBytes} {
		path, data := writeFixture(t, dir, name, size)
		fixtures[name] = data
		files = append(files, client.BatchFile{Path: path, Parent: folder.ID})
	}
	large, _ := writeFixture(t, dir, "large.bin", client.MaxBatchFileBytes+1)
	link := filepath.Join(filepath.Dir(large), "link")
	if err := os.Symlink(large, link); err != nil {
		t.Fatal(err)
	}
	files = append(files, client.BatchFile{Path: large, Parent: folder.ID}, client.BatchFile{Path: link, Parent: folder.ID},
		client.BatchFile{Path: filepath.Join(dir, "missing"), Parent: folder.ID})

	outcomes, err := c.UploadBatch(t.Context(), files)
	if err != nil || len(outcomes) != len(files) {
		t.Fatalf("batch: %d outcomes, %v", len(outcomes), err)
	}
	for i, outcome := range outcomes[:4] {
		if outcome.Err != nil || outcome.Result.State != protocol.UploadCommitted {
			t.Fatalf("file %d: %+v", i, outcome)
		}
	}
	if _, skipped := source.Skipped(outcomes[5].Err); outcomes[4].Err == nil || !skipped || outcomes[6].Err == nil {
		t.Fatalf("refusals: %v / %v / %v", outcomes[4].Err, outcomes[5].Err, outcomes[6].Err)
	}

	page, err := c.CatalogFolder(t.Context(), folder.ID)
	if err != nil || len(page.Entries) != len(fixtures) {
		t.Fatalf("catalog: %+v %v", page, err)
	}
	restored := t.TempDir()
	for _, entry := range page.Entries {
		destination := filepath.Join(restored, entry.Standard.Name)
		if err := c.Download(t.Context(), entry, destination, nil); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(destination); err != nil || !bytes.Equal(data, fixtures[entry.Standard.Name]) {
			t.Fatalf("restored %s differs: %v", entry.Standard.Name, err)
		}
	}

	// Recover a crash after batch publication but before its result was saved.
	var group sync.WaitGroup
	for i := range 8 {
		path, _ := writeFixture(t, dir, fmt.Sprintf("durable-%d", i), 1024)
		group.Go(func() {
			var cp client.UploadCheckpoint
			var saved []byte
			lost := errors.New("simulated exit before result checkpoint")
			_, err := c.UploadResumable(t.Context(), path, folder.ID, &cp, func(cp *client.UploadCheckpoint) error {
				if cp.Result != nil {
					return lost
				}
				saved, _ = json.Marshal(cp)
				return nil
			}, nil)
			if !errors.Is(err, lost) {
				t.Errorf("publication: %v", err)
				return
			}
			result := *cp.Result
			cp = client.UploadCheckpoint{}
			if err := json.Unmarshal(saved, &cp); err != nil {
				t.Error(err)
				return
			}
			recovered, err := c.UploadResumable(t.Context(), path, folder.ID, &cp, func(*client.UploadCheckpoint) error { return nil }, nil)
			if err != nil || recovered != result {
				t.Errorf("recovery changed result: %+v / %+v, %v", result, recovered, err)
				return
			}
			history, err := c.History(t.Context(), result.FileID, "")
			if err != nil || len(history.Versions) != 1 {
				t.Errorf("recovery duplicated version: %+v, %v", history, err)
			}
		})
	}
	group.Wait()

	// Deleting the folder removes every batched object.
	if err := c.DeleteEntry(t.Context(), folder.ID); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(filepath.Join(config.DataDir, "objects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			t.Errorf("object left after deletion: %s", path)
		}
		return nil
	})
	outcomes, err = c.UploadBatch(t.Context(), files[:1])
	var refused api.Error
	if !errors.As(err, &refused) || refused.Code != api.Conflict {
		t.Fatalf("upload into deleted folder: %v %v", outcomes, err)
	}
}

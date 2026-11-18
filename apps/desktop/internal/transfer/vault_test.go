package transfer

import (
	"bytes"
	"database/sql"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
	"github.com/chironjit/serversend/shared/vault"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateQueueEncryptionLockAndCheckpoint(t *testing.T) {
	s, scope := queueFixture(t)
	k := vault.RandomKey()
	locked := false
	s.key = func(want protocol.Scope) (vault.Key, error) {
		if locked || want != scope {
			return vault.Key{}, vault.ErrLocked
		}
		return k, nil
	}
	_, err := s.create(t.Context(), scope, "secret-batch-canary", []durableTask{{Kind: "upload", Name: "secret-name-canary", Payload: durablePayload{Private: true, SealedScope: scope, Path: "/secret-path-canary/file"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.claim(t.Context(), scope)
	if err != nil || task.Payload.Path != "/secret-path-canary/file" {
		t.Fatal(err)
	}
	locked = true
	task.Done = 7
	if err = s.save(t.Context(), task); err != nil {
		t.Fatal("checkpoint must survive lock", err)
	}
	if err = s.finish(t.Context(), task, "waiting_vault", "secret-error-canary", 0); err != nil {
		t.Fatal(err)
	}
	_, err = s.create(t.Context(), scope, "standard", []durableTask{{Kind: "upload", Name: "standard", Payload: durablePayload{Path: "standard"}}})
	if err != nil {
		t.Fatal(err)
	}
	standard, err := s.claim(t.Context(), scope)
	if err != nil || standard.Payload.Private {
		t.Fatal("locked vault blocks standard work", err)
	}
	if _, err = s.claim(t.Context(), scope); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("locked private work claimed", err)
	}
	locked = false
	resumed, err := s.claim(t.Context(), scope)
	if err != nil || resumed.Done != 7 || resumed.Payload.Path != task.Payload.Path {
		t.Fatal("private resume", err)
	}
	if err = s.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(new(int), new(int), new(int)); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, e := os.ReadFile(filepath.Join(s.dir, "desktop.sqlite") + suffix)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		for _, canary := range []string{"secret-batch-canary", "secret-name-canary", "secret-path-canary", "secret-error-canary"} {
			if bytes.Contains(raw, []byte(canary)) {
				t.Fatal("plaintext queue leak", canary)
			}
		}
	}
}

func TestPrivateSchedulerLeavesStandardWorkersAvailable(t *testing.T) {
	s, scope := queueFixture(t)
	key := vault.RandomKey()
	s.key = func(protocol.Scope) (vault.Key, error) { return key, nil }
	_, err := s.create(t.Context(), scope, "private", []durableTask{{Kind: "upload", Payload: durablePayload{Private: true, SealedScope: scope, Path: "secret-one"}}, {Kind: "upload", Payload: durablePayload{Private: true, SealedScope: scope, Path: "secret-two"}}})
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.claim(t.Context(), scope)
	if err != nil || !active.Payload.Private {
		t.Fatal(err)
	}
	_, err = s.create(t.Context(), scope, "standard", []durableTask{{Kind: "upload", Payload: durablePayload{Path: "standard"}}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.claimWithPrivate(t.Context(), scope, false, active.ID)
	if err != nil || next.Payload.Private {
		t.Fatal("another private task occupied a standard worker", err)
	}
}

func TestOversizedPrivateFileSkipsAndCanUploadUnencrypted(t *testing.T) {
	s, scope := queueFixture(t)
	key := vault.RandomKey()
	locked := false
	s.key = func(protocol.Scope) (vault.Key, error) {
		if locked {
			return vault.Key{}, vault.ErrLocked
		}
		return key, nil
	}
	q := &durableQueue{store: s}
	large := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(large, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, client.MaxPrivateFileBytes+1); err != nil {
		t.Fatal(err)
	}
	stage := q.stagePath(1)
	err := q.privateStageRoom(large, stage)
	if skip, ok := source.Skipped(err); !ok || skip.Reason != client.PrivateTooLargeReason {
		t.Fatal("oversized private file was not skipped", err)
	}
	// Paused stages count against the budget for a fresh file.
	if err = os.Truncate(large, 1); err != nil {
		t.Fatal(err)
	}
	paused := q.stagePath(2)
	if err = os.MkdirAll(filepath.Dir(paused), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(paused, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Truncate(paused, privateStageBudget); err != nil {
		t.Fatal(err)
	}
	var typed api.Error
	if err = q.privateStageRoom(large, stage); !errors.As(err, &typed) || typed.Message != privateBudgetFull {
		t.Fatal("staging budget not enforced", err)
	}
	if err = os.Remove(paused); err != nil {
		t.Fatal(err)
	}
	if err = q.privateStageRoom(large, stage); err != nil {
		t.Fatal(err)
	}

	batch, err := s.create(t.Context(), scope, "Private batch", []durableTask{{Kind: "upload", Name: "Private item", Payload: durablePayload{Private: true, SealedScope: scope, Path: large, Parent: "parent"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finish(t.Context(), task, "skipped", client.PrivateTooLargeReason, 0); err != nil {
		t.Fatal(err)
	}
	batches, err := s.batches(t.Context(), scope)
	if err != nil || len(batches) != 1 || batches[0].Oversized != 1 || len(batches[0].SkippedItems) != 1 {
		t.Fatalf("oversized skip not reported: %+v %v", batches, err)
	}
	if item := batches[0].SkippedItems[0]; item.ID != task.ID || !item.Unencrypted || item.Reason != client.PrivateTooLargeReason {
		t.Fatalf("skipped item: %+v", item)
	}
	// Keeping the skip is recorded and ends the offer for that file.
	if err = s.control(t.Context(), scope, task.ID, false, "keep_skipped"); err != nil {
		t.Fatal(err)
	}
	if batches, err = s.batches(t.Context(), scope); err != nil || batches[0].Oversized != 0 || batches[0].SkippedItems[0].Unencrypted {
		t.Fatalf("kept skip still offered: %+v %v", batches, err)
	}
	if _, err = s.db.Exec(`UPDATE transfer_tasks SET error=? WHERE id=?`, client.PrivateTooLargeReason, task.ID); err != nil {
		t.Fatal(err)
	}
	locked = true
	if err = s.control(t.Context(), scope, batch, true, "unencrypted"); !errors.Is(err, vault.ErrLocked) {
		t.Fatal("unencrypted upload must need the vault to read the path", err)
	}
	locked = false
	if err = s.control(t.Context(), scope, batch, true, "unencrypted"); err != nil {
		t.Fatal(err)
	}
	standard, err := s.claim(t.Context(), scope)
	if err != nil || standard.ID != task.ID || standard.Payload.Private || standard.Payload.Path != large || standard.Payload.Parent != "parent" || standard.Name != "large.bin" {
		t.Fatalf("requeued task: %+v %v", standard, err)
	}
	if err = s.control(t.Context(), scope, task.ID, false, "unencrypted"); err == nil {
		t.Fatal("a task already requeued was requeued again")
	}
}

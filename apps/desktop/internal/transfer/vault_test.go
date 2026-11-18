package transfer

import (
	"bytes"
	"database/sql"
	"errors"
	"github.com/chironjit/serversend/shared/protocol"
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

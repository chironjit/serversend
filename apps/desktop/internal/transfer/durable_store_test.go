package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

func queueFixture(t *testing.T) (*durableStore, protocol.Scope) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := openDurable(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store, protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}
}
func TestDurableSchedulerFairnessWaitsAndScope(t *testing.T) {
	s, scope := queueFixture(t)
	tasks := []durableTask{{Kind: "upload", Name: "a", Payload: durablePayload{Path: "a"}}, {Kind: "upload", Name: "b", Payload: durablePayload{Path: "b"}}}
	first, err := s.create(t.Context(), scope, "first", tasks)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.create(t.Context(), scope, "second", tasks)
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if one.BatchID != first || two.BatchID != second {
		t.Fatalf("scheduler monopolized one batch: %d %d", one.BatchID, two.BatchID)
	}
	if err := s.finish(t.Context(), one, "waiting_network", "offline", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	other := scope
	other.AccountID = "33333333333333333333333333333333"
	if _, err := s.claim(t.Context(), other); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-account claim: %v", err)
	}
	if err := s.control(t.Context(), other, second, true, "cancel"); err == nil {
		t.Fatal("wrong-account control accepted")
	}
	var state string
	if err := s.db.QueryRow(`SELECT state FROM transfer_tasks WHERE id=?`, two.ID).Scan(&state); err != nil || state != "active" {
		t.Fatalf("cross-account control changed task: %s %v", state, err)
	}
	// A resumed task that still has an interrupted old worker is excluded until
	// that worker has returned, without holding up another eligible task.
	if err := s.control(t.Context(), scope, two.ID, false, "pause"); err != nil {
		t.Fatal(err)
	}
	if err := s.control(t.Context(), scope, two.ID, false, "resume"); err != nil {
		t.Fatal(err)
	}
	next, err := s.claim(t.Context(), scope, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == two.ID || next.ID == one.ID {
		t.Fatal("claimed interrupted or not-yet-due work")
	}
}
func TestDurableStateDecisionsWinLateAcknowledgements(t *testing.T) {
	for _, action := range []string{"pause", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, scope := queueFixture(t)
			batch, err := s.create(t.Context(), scope, "selection", []durableTask{{Kind: "upload", Name: "file", Payload: durablePayload{Path: "file"}}})
			if err != nil {
				t.Fatal(err)
			}
			task, err := s.claim(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.control(t.Context(), scope, batch, true, action); err != nil {
				t.Fatal(err)
			}
			task.Done, task.Total = 4096, 8192
			if err := s.save(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if err := s.finish(t.Context(), task, "done", "", 0); err != nil {
				t.Fatal(err)
			}
			items, err := s.items(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			want := "paused"
			if action == "cancel" {
				want = "cancel_pending"
			}
			if len(items) != 1 || items[0].State != want || items[0].Done != 4096 {
				t.Fatalf("late ack undid decision: %+v", items)
			}
		})
	}
}
func TestDurableQueueBoundedViewAndIdempotentDiscovery(t *testing.T) {
	s, scope := queueFixture(t)
	batch, err := s.create(t.Context(), scope, "large", []durableTask{{Kind: "scan_upload", Name: "root", Payload: durablePayload{Path: "root"}}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	children := make([]durableTask, 128)
	for page := 0; page < 10; page++ {
		for i := range children {
			path := fmt.Sprintf("root/%05d", page*128+i)
			children[i] = durableTask{Kind: "upload", Name: filepath.Base(path), Payload: durablePayload{Path: path}}
		}
		if err := s.children(t.Context(), parent, children); err != nil {
			t.Fatal(err)
		}
		if err := s.children(t.Context(), parent, children); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM transfer_tasks WHERE batch_id=?`, batch).Scan(&count); err != nil || count != 1281 {
		t.Fatalf("discovery count=%d err=%v", count, err)
	}
	items, err := s.items(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 200 {
		t.Fatalf("view unbounded: %d", len(items))
	}
	found := false
	for _, item := range items {
		if item.ID == parent.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("active older task hidden behind recent queued work")
	}
	summaries, err := s.batches(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Files != 1280 {
		t.Fatalf("incomplete total: %+v", summaries)
	}
}
func TestBandwidthChangeUnblocksActiveThrottle(t *testing.T) {
	s, scope := queueFixture(t)
	_ = scope
	q := &durableQueue{store: s, bandwidth: 1, rateChanged: make(chan struct{})}
	m := &Manager{ctx: t.Context(), durable: q}
	q.manager = m
	done := make(chan struct{})
	go func() { q.throttle(t.Context(), 4096); close(done) }()
	time.Sleep(20 * time.Millisecond)
	if err := m.SetBandwidth(0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bandwidth control did not release old reservation")
	}
	var got int64
	if err := s.db.QueryRow(`SELECT value FROM transfer_preferences WHERE key='bandwidth'`).Scan(&got); err != nil || got != 0 {
		t.Fatalf("bandwidth not durable: %d %v", got, err)
	}
}

func TestSlowWindowPendingMapIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	received := make(chan int, 2)
	emit := coalesce(ctx, func(items []Item) { <-release; received <- len(items) })
	emit(Item{ID: 1})
	time.Sleep(20 * time.Millisecond)
	for i := 2; i < 10000; i++ {
		emit(Item{ID: i})
	}
	close(release)
	<-received
	if n := <-received; n > 200 {
		t.Fatalf("pending events unbounded: %d", n)
	}
}

func TestProductionSchedulerPauseResumeAndSkippedReservationRelease(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprint(skip), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "source")
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 8192), 0600); err != nil {
				t.Fatal(err)
			}
			fixture := &durableUploadFixture{puts: map[int64]int{}, blockSecond: true, blocked: make(chan struct{})}
			server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.handler))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			sum := sha256.Sum256(server.Certificate().Raw)
			c := durableFixtureClient(t, server.URL, hex.EncodeToString(sum[:]))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m := &Manager{ctx: ctx, client: c, emit: func(Item) {}}
			if err := m.enableDurable(config); err != nil {
				t.Fatal(err)
			}
			defer func() { cancel(); m.durable.wg.Wait(); <-m.durable.closed }()
			if err := m.Upload([]string{path}, ""); err != nil {
				t.Fatal(err)
			}
			select {
			case <-fixture.blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("scheduler did not run upload")
			}
			items := m.Transfers()
			if len(items) != 1 {
				t.Fatal(items)
			}
			id := items[0].ID
			if err := m.QueueAction(id, false, "pause"); err != nil {
				t.Fatal(err)
			}
			items = m.Transfers()
			if items[0].State != "paused" || items[0].Done != 4096 {
				t.Fatalf("pause checkpoint: %+v", items)
			}
			if skip {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing", path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := m.QueueAction(id, false, "resume"); err != nil {
				t.Fatal(err)
			}
			want := "done"
			if skip {
				want = "skipped"
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				items = m.Transfers()
				if items[0].State == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("resume outcome: %+v queue health=%s", items, m.QueueHealth())
				}
				time.Sleep(10 * time.Millisecond)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.puts[0] != 1 {
				t.Fatalf("repeated acknowledged bytes: %v", fixture.puts)
			}
			if skip && fixture.state != protocol.UploadCancelled {
				t.Fatal("skipped resumed source retained reservation")
			}
		})
	}
}

func TestSecondQueueInitializationCannotRecoverActiveOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	store, err := openDurable(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	scope := protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}
	if _, err := store.create(t.Context(), scope, "owned", []durableTask{{Kind: "upload", Name: "file", Payload: durablePayload{Path: "file"}}}); err != nil {
		t.Fatal(err)
	}
	task, err := store.claim(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{ctx: t.Context()}
	if err := m.enableDurable(dir); err == nil {
		t.Fatal("second process entered recovery before single-instance negotiation")
	}
	var state string
	if err := store.db.QueryRow(`SELECT state FROM transfer_tasks WHERE id=?`, task.ID).Scan(&state); err != nil || state != "active" {
		t.Fatalf("second startup reset owner state: %s %v", state, err)
	}
}

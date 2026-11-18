package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Wake interrupts a request that may have hung across sleep and resumes from
// the checkpoint; a global pause holds work across restart; quitting stops
// workers and closes the queue.
func TestBackgroundWakePauseAndQuit(t *testing.T) {
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
	open := func() (*Manager, context.CancelFunc) {
		ctx, cancel := context.WithCancel(t.Context())
		m := &Manager{ctx: ctx, client: c, emit: func(Item) {}}
		if err := m.enableDurable(config); err != nil {
			t.Fatal(err)
		}
		return m, cancel
	}
	wait := func(m *Manager, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			items := m.Transfers()
			if len(items) > 0 && items[0].State == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("want %s: %+v %s", want, items, m.QueueHealth())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	m, cancel := open()
	if err := m.Upload([]string{path}, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	if err := m.Wake(); err != nil {
		t.Fatal(err)
	}
	wait(m, "done")
	fixture.mu.Lock()
	if fixture.puts[0] != 1 {
		t.Fatalf("wake repeated acknowledged bytes: %v", fixture.puts)
	}
	fixture.mu.Unlock()

	if err := m.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("y"), 8192), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Upload([]string{path}, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if items := m.Transfers(); items[0].State != "queued" {
		t.Fatalf("paused queue ran: %+v", items)
	}
	cancel()
	select {
	case <-m.Stopped():
	case <-time.After(5 * time.Second):
		t.Fatal("queue did not stop")
	}

	m, cancel = open()
	defer func() { cancel(); <-m.Stopped() }()
	if !m.Paused() {
		t.Fatal("global pause not durable")
	}
	if err := m.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	// The single-upload fixture rejects a second identity; leaving the queue
	// shows the held task was released.
	deadline := time.Now().Add(5 * time.Second)
	for m.Transfers()[0].State == "queued" {
		if time.Now().After(deadline) {
			t.Fatal("resume did not release held work")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

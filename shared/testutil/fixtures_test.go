package testutil

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDeterministicClockAndEntropy(t *testing.T) {
	clock := NewClock(time.Unix(0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Go(func() { clock.Advance(time.Second); _ = clock.Now() })
	}
	wg.Wait()
	if !clock.Now().Equal(time.Unix(10, 0)) {
		t.Fatal("clock lost updates")
	}
	a, b := make([]byte, 32), make([]byte, 32)
	if _, err := io.ReadFull(NewRandom(42), a); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(NewRandom(42), b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("random source not deterministic")
	}
	if _, err := io.ReadFull(bytes.NewReader(nil), a); !errors.Is(err, io.EOF) {
		t.Fatal("finite entropy did not fail")
	}
}

func TestFilesystemFailureAndRetry(t *testing.T) {
	injected := errors.New("injected disk full")
	faults := &Faults{}
	fs := Filesystem{Faults: faults}
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")
	faults.Arm("open", 1, os.ErrPermission)
	if _, err := fs.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("open failure: %v", err)
	}
	file, err := fs.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	short := ShortWriter{Writer: file, Remaining: 3, Err: injected}
	if n, err := short.Write([]byte("abcdef")); n != 3 || !errors.Is(err, injected) {
		t.Fatalf("partial write n=%d err=%v", n, err)
	}
	faults.Arm("write", 1, injected)
	if n, err := file.Write([]byte("def")); n != 0 || !errors.Is(err, injected) {
		t.Fatal("write failure ignored")
	}
	if _, err := file.Write([]byte("def")); err != nil {
		t.Fatal(err)
	}
	faults.Arm("sync", 2, injected)
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); !errors.Is(err, injected) {
		t.Fatal("nth sync failure ignored")
	}
	if err := file.Sync(); err != nil {
		t.Fatal("retry failed")
	}
	faults.Arm("close", 1, injected)
	if err := file.Close(); !errors.Is(err, injected) {
		t.Fatal("close failure ignored")
	}
	destination := filepath.Join(dir, "committed")
	faults.Arm("rename", 1, injected)
	if err := fs.Rename(path, destination); !errors.Is(err, injected) {
		t.Fatal("rename failure ignored")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed rename published data")
	}
	if err := fs.Rename(path, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "abcdef" {
		t.Fatalf("restart contents=%q error=%v", got, err)
	}
	reader, err := fs.OpenFile(destination, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	faults.Arm("read", 1, injected)
	var sink bytes.Buffer
	if _, err := io.Copy(&sink, reader); !errors.Is(err, injected) {
		t.Fatalf("io.Copy bypassed read fault: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := fs.OpenFile(destination, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	faults.Arm("write", 1, injected)
	if _, err := io.Copy(writer, bytes.NewBufferString("no write")); !errors.Is(err, injected) {
		t.Fatalf("io.Copy bypassed write fault: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	faults.Arm("remove", 1, injected)
	if err := fs.Remove(destination); !errors.Is(err, injected) {
		t.Fatal("remove failure ignored")
	}
	if err := fs.Remove(destination); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkReadAndWriteInterruption(t *testing.T) {
	for _, direction := range []string{"read", "write"} {
		t.Run(direction, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			deadline := time.Now().Add(5 * time.Second)
			if err := a.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			if err := b.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			if direction == "read" {
				conn := NewInterruptConn(a, 3, -1)
				done := make(chan struct{})
				go func() { defer close(done); _, _ = b.Write([]byte("abcdef")) }()
				got := make([]byte, 6)
				n, err := io.ReadFull(conn, got)
				if n != 3 || !errors.Is(err, ErrInterrupted) || string(got[:n]) != "abc" {
					t.Fatalf("read n=%d data=%q error=%v", n, got[:n], err)
				}
				a.Close()
				<-done
			} else {
				conn := NewInterruptConn(a, -1, 3)
				received := make(chan []byte, 1)
				go func() { p := make([]byte, 3); _, _ = io.ReadFull(b, p); received <- p }()
				n, err := conn.Write([]byte("abcdef"))
				if n != 3 || !errors.Is(err, ErrInterrupted) || string(<-received) != "abc" {
					t.Fatalf("write n=%d error=%v", n, err)
				}
				if n, err := conn.Write([]byte("def")); n != 0 || !errors.Is(err, ErrInterrupted) {
					t.Fatal("connection silently resumed")
				}
			}
		})
	}
}

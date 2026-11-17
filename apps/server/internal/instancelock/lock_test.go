package instancelock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExclusiveReleaseAndPartialRollback(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	held, err := Acquire(second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if other, err := Acquire(first, second); err == nil {
		other.Close()
		t.Fatal("accepted overlapping deployment")
	}
	// Failure on the second path must release the first path immediately.
	available, err := Acquire(first)
	if err != nil {
		t.Fatal("partial acquisition leaked a lock:", err)
	}
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	if err := available.Close(); err != nil {
		t.Fatal("repeated close:", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	available, err = Acquire(first, second)
	if err != nil {
		t.Fatal("clean shutdown did not release locks:", err)
	}
	available.Close()
	if _, err := os.Stat(filepath.Join(first, Filename)); err != nil {
		t.Fatal("lock file must remain to prevent inode replacement:", err)
	}
}

func TestInvalidLockPaths(t *testing.T) {
	for _, path := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		if locks, err := Acquire(path); err == nil {
			locks.Close()
			t.Fatal("accepted invalid path", path)
		}
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, Filename), 0o700); err != nil {
		t.Fatal(err)
	}
	if locks, err := Acquire(directory); err == nil {
		locks.Close()
		t.Fatal("accepted directory in place of lock file")
	}
}

func TestLockRefusesSymlink(t *testing.T) {
	directory, targetDirectory := t.TempDir(), t.TempDir()
	target := filepath.Join(targetDirectory, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, Filename)); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if locks, err := Acquire(directory); err == nil {
		locks.Close()
		t.Fatal("accepted symlink lock")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "unchanged" {
		t.Fatal("changed link target", err)
	}
}

func TestProcessCrashReleasesLocks(t *testing.T) {
	if directory := os.Getenv("SERVERSEND_LOCK_CHILD"); directory != "" {
		locks, err := Acquire(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer locks.Close()
		os.Stdout.WriteString("locked\n")
		// Wait on stdin rather than a timer; the parent forcibly terminates us.
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
		return
	}
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestProcessCrashReleasesLocks$")
	command.Env = append(os.Environ(), "SERVERSEND_LOCK_CHILD="+directory)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child could not acquire lock: %q %v", line, err)
	}
	if locks, err := Acquire(directory); err == nil {
		locks.Close()
		t.Fatal("accepted a directory held by another process")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exited *exec.ExitError
	if err := command.Wait(); !errors.As(err, &exited) {
		t.Fatalf("child not forcibly terminated: %v", err)
	}
	locks, err := Acquire(directory)
	if err != nil {
		t.Fatal("crashed process retained its lock:", err)
	}
	locks.Close()
}

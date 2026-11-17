package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/source"
)

func restoreTestPath(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, name)
}

func TestRestoreNamesAndCollisionKeys(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "a?b", "a.", "a ", "nul", "COM1.txt", "LPT¹", "CONOUT$", ".serversend-file.partial", "a\x00b", strings.Repeat("a", 256)} {
		if ValidateRestoreName(name) == nil {
			t.Errorf("unsafe name accepted: %q", name)
		}
	}
	for _, name := range []string{"file.txt", "A normal file", "é", "東京", "COM10"} {
		if err := ValidateRestoreName(name); err != nil {
			t.Errorf("safe name %q: %v", name, err)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := CanonicalRestorePath(filepath.Join(root, "CAFÉ.txt"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalRestorePath(filepath.Join(root, "cafe\u0301.TXT"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("case/normalization collision not detected")
	}
}

func TestDownloadCollisionPoliciesNeverOverwriteUnexpectedFiles(t *testing.T) {
	for _, policy := range []string{"skip", "fail"} {
		t.Run(policy, func(t *testing.T) {
			entry, manifest, data := downloadFixture()
			destination := restoreTestPath(t, "restored")
			c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/manifest") {
					return recoveryResponse(manifest), nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
			}))
			err := c.DownloadWithPolicy(t.Context(), entry, destination, policy, func(done, total int64) {
				if done == total {
					if e := os.WriteFile(destination, []byte("appeared during restore"), 0600); e != nil {
						t.Fatal(e)
					}
				}
			})
			if policy == "fail" && !errors.Is(err, ErrDestinationExists) {
				t.Fatalf("collision: %v", err)
			}
			if policy == "skip" {
				if _, ok := source.Skipped(err); !ok {
					t.Fatalf("skip: %v", err)
				}
			}
			got, _ := os.ReadFile(destination)
			if string(got) != "appeared during restore" {
				t.Fatal("concurrent destination overwritten")
			}
		})
	}
}

func TestRestoreRefusesAncestorsAndLocalNameAliases(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(elsewhere, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	entry, _, _ := downloadFixture()
	c := recoveryClient(sourceRequestGuard{t: t})
	if err := c.DownloadWithPolicy(t.Context(), entry, filepath.Join(alias, "file"), "replace", nil); err == nil {
		t.Fatal("followed destination ancestor")
	}
	if err := os.WriteFile(filepath.Join(root, "NAME"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadWithPolicy(t.Context(), entry, filepath.Join(root, "name"), "replace", nil); err == nil {
		t.Fatal("accepted local case alias")
	}
}

func TestRestoreDestinationLockHonorsCancellation(t *testing.T) {
	destination := restoreTestPath(t, "restored")
	release, err := lockDownloadDestination(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if unlock, err := lockDownloadDestination(ctx, destination); !errors.Is(err, context.Canceled) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("lock cancellation: %v", err)
	}
}

func TestRestoreSurvivesProcessExit(t *testing.T) {
	entry, manifest, data := downloadFixture()
	if destination := os.Getenv("SERVERSEND_RESTORE_EXIT_DESTINATION"); destination != "" {
		c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/manifest") {
				return recoveryResponse(manifest), nil
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
		}))
		_ = c.DownloadWithPolicy(t.Context(), entry, destination, "replace", func(done, total int64) {
			if done == 4096 {
				os.Exit(23)
			}
		})
		os.Exit(24)
	}
	destination := restoreTestPath(t, "restored")
	command := exec.Command(os.Args[0], "-test.run=^TestRestoreSurvivesProcessExit$")
	command.Env = append(os.Environ(), "SERVERSEND_RESTORE_EXIT_DESTINATION="+destination)
	err := command.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("child did not reach checkpoint: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("incomplete destination became visible")
	}
	fetched := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		fetched++
		if !strings.HasSuffix(r.URL.Path, "/chunks/1") {
			t.Fatal("acknowledged checkpoint chunk fetched again")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
	}))
	if err := c.DownloadWithPolicy(t.Context(), entry, destination, "replace", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(destination)
	if !bytes.Equal(got, data) || fetched != 1 {
		t.Fatalf("process resume: fetched=%d bytes=%d", fetched, len(got))
	}
}

func TestCorruptRestoreNeverPublishesAndCanResume(t *testing.T) {
	entry, manifest, data := downloadFixture()
	destination := restoreTestPath(t, "restored")
	corrupt := true
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		body := recoveryChunk(r, data)
		if corrupt && strings.HasSuffix(r.URL.Path, "/chunks/1") {
			body = bytes.Repeat([]byte("x"), 4096)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}))
	if err := c.DownloadWithPolicy(t.Context(), entry, destination, "fail", nil); err == nil {
		t.Fatal("corruption accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("corrupt destination published")
	}
	corrupt = false
	if err := c.DownloadWithPolicy(t.Context(), entry, destination, "fail", nil); err != nil {
		t.Fatal(err)
	}
	match, err := c.DownloadMatches(t.Context(), entry, destination)
	if err != nil || !match {
		t.Fatalf("publication reconciliation: match=%v err=%v", match, err)
	}
	if err := os.WriteFile(destination, bytes.Repeat([]byte("x"), len(data)), 0600); err != nil {
		t.Fatal(err)
	}
	match, err = c.DownloadMatches(t.Context(), entry, destination)
	if err != nil || match {
		t.Fatalf("changed destination accepted: match=%v err=%v", match, err)
	}
}

func TestFailedPublicationRetainsVerifiedCheckpoint(t *testing.T) {
	entry, manifest, data := downloadFixture()
	destination := restoreTestPath(t, "restored")
	fetched := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		fetched++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
	}))
	err := c.DownloadWithPolicy(t.Context(), entry, destination, "replace", func(done, total int64) {
		if done == total {
			if e := os.Mkdir(destination, 0700); e != nil {
				t.Fatal(e)
			}
		}
	})
	if err == nil {
		t.Fatal("overwrote directory appearing at publication")
	}
	if info, e := os.Stat(destination); e != nil || !info.IsDir() {
		t.Fatal("existing directory was modified")
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadWithPolicy(t.Context(), entry, destination, "replace", nil); err != nil {
		t.Fatal(err)
	}
	if fetched != 2 {
		t.Fatalf("verified checkpoint fetched again: requests=%d", fetched)
	}
	got, _ := os.ReadFile(destination)
	if !bytes.Equal(got, data) {
		t.Fatal("restored bytes differ")
	}
}

func TestCancelledPublicationKeepsCheckpointWithoutDestination(t *testing.T) {
	entry, manifest, data := downloadFixture()
	destination := restoreTestPath(t, "restored")
	fetched := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		fetched++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	err := c.DownloadWithPolicy(ctx, entry, destination, "fail", func(done, total int64) {
		if done == total {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("cancelled file was published")
	}
	if err := c.DownloadWithPolicy(t.Context(), entry, destination, "fail", nil); err != nil {
		t.Fatal(err)
	}
	if fetched != 2 {
		t.Fatalf("verified checkpoint refetched: %d", fetched)
	}
}

func TestCancelDownloadRemovesOnlyScopedCheckpoint(t *testing.T) {
	entry, manifest, data := downloadFixture()
	destination := restoreTestPath(t, "restored")
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	if err := c.DownloadWithPolicy(ctx, entry, destination, "fail", func(done, total int64) {
		if done == 4096 {
			cancel()
		}
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	partial := filepath.Join(filepath.Dir(destination), downloadPartialName(manifest.Scope, entry, filepath.Base(destination)))
	otherScope := manifest.Scope
	otherScope.AccountID = "99999999999999999999999999999999"
	other := filepath.Join(filepath.Dir(destination), downloadPartialName(otherScope, entry, filepath.Base(destination)))
	if err := os.WriteFile(other, []byte("other account checkpoint"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing destination"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelDownload(t.Context(), entry, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("cancelled checkpoint retained")
	}
	if got, err := os.ReadFile(other); err != nil || string(got) != "other account checkpoint" {
		t.Fatal("other scope checkpoint modified")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "existing destination" {
		t.Fatal("published destination modified")
	}
	if err := c.CancelDownload(t.Context(), entry, destination); err != nil {
		t.Fatal("cancellation is not idempotent:", err)
	}
}

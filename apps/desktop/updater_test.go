package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestReleaseSelectionAndChecksums(t *testing.T) {
	payload := []byte("release bytes")
	digest := sha256.Sum256(payload)
	for _, tc := range []struct {
		name, sums string
		wantErr    bool
	}{
		{"valid", fmt.Sprintf("%x  serversend-desktop-2.0.0-linux-amd64.AppImage\n", digest), false},
		{"missing sidecar", "", true},
		{"missing entry", fmt.Sprintf("%x  other.AppImage\n", digest), true},
		{"short digest", "abcd  serversend-desktop-2.0.0-linux-amd64.AppImage\n", true},
		{"malformed digest", "xyz  serversend-desktop-2.0.0-linux-amd64.AppImage\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/test/repo/releases/latest":
					assets := []map[string]any{{"name": "serversend-desktop-2.0.0-linux-amd64.AppImage", "browser_download_url": base + "/payload", "size": len(payload)}}
					if tc.sums != "" {
						assets = append(assets, map[string]any{"name": "SHA256SUMS", "browser_download_url": base + "/sums"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v2.0.0", "body": "Release notes", "assets": assets})
				case "/sums":
					_, _ = w.Write([]byte(tc.sums))
				case "/payload":
					_, _ = w.Write([]byte("tampered download"))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			base = srv.URL
			gh, err := github.New(github.Config{Repository: "test/repo", BaseURL: base, ChecksumAsset: "SHA256SUMS", AssetMatcher: desktopAsset})
			if err != nil {
				t.Fatal(err)
			}
			p := checksumProvider{gh}
			req := updater.CheckRequest{CurrentVersion: "1.0.0", Platform: "linux", Arch: "amd64"}
			rel, err := p.Check(context.Background(), req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("release=%v error=%v", rel, err)
			}
			if tc.wantErr {
				return
			}
			if rel.Version != "2.0.0" || rel.Notes != "Release notes" || hex.EncodeToString(rel.Verification.Digest) != hex.EncodeToString(digest[:]) {
				t.Fatalf("unexpected release: %+v", rel)
			}
			u := updater.New(updateTestHost{})
			if err := u.Init(updater.Config{CurrentVersion: "1.0.0", Platform: "linux", Arch: "amd64", Providers: []updater.Provider{p}, Window: updater.WindowNone}); err != nil {
				t.Fatal(err)
			}
			if _, err := u.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := u.DownloadAndInstall(context.Background()); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
				t.Fatalf("tampered payload: %v", err)
			}
			if u.DownloadedPath() != "" || u.State() != updater.StateError {
				t.Fatal("tampered payload was staged for restart")
			}
			req.CurrentVersion = "2.0.0"
			if rel, err := p.Check(context.Background(), req); err != nil || rel != nil {
				t.Fatalf("offered same version: %v, %v", rel, err)
			}
		})
	}
}

func TestDesktopAsset(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "server-linux-amd64.AppImage"},
		{Name: "serversend-desktop-2.0.0-linux-amd64.tar.gz"},
		{Name: "serversend-desktop-2.0.0-linux-arm64.AppImage"},
		{Name: "serversend-desktop-2.0.0-linux-amd64.AppImage.sha256"},
		{Name: "serversend-desktop-2.0.0-linux-amd64.AppImage"},
		{Name: "serversend-desktop-2.0.0-windows-amd64.zip"},
		{Name: "serversend-desktop-2.0.0-darwin-arm64.zip"},
	}
	for _, tc := range []struct {
		os, arch string
		want     int
	}{{"linux", "amd64", 4}, {"windows", "amd64", 5}, {"darwin", "arm64", 6}, {"darwin", "amd64", -1}} {
		req := updater.CheckRequest{Platform: tc.os, Arch: tc.arch}
		if got := desktopAsset(req, assets); got != tc.want {
			t.Fatalf("%s/%s: %d, want %d", tc.os, tc.arch, got, tc.want)
		}
	}
	req := updater.CheckRequest{Platform: "linux", Arch: "amd64"}
	if desktopAsset(req, append(assets, assets[4])) != -1 {
		t.Fatal("accepted ambiguous payloads")
	}
}

// No window or process replacement is permitted in provider integration tests.
type updateTestHost struct{}

func (updateTestHost) Emit(string, ...any) bool         { return false }
func (updateTestHost) OnEvent(string, func(any)) func() { return func() {} }
func (updateTestHost) OpenWindow(updater.WindowOptions) updater.WindowHandle {
	panic("unexpected update window")
}
func (updateTestHost) Quit() { panic("unexpected restart") }

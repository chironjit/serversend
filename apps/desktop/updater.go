package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// Release builds set these with -ldflags. Development binaries cannot update.
var currentVersion = "dev"
var updateRepository = "chironjit/serversend"

// GitHub's checksum sidecar is optional upstream. ServerSend requires a valid
// SHA-256 entry before offering any release for installation.
type checksumProvider struct{ updater.Provider }

func (p checksumProvider) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	rel, err := p.Provider.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	v := rel.Verification
	if v == nil || v.DigestAlgo != "sha256" || len(v.Digest) != sha256.Size {
		return nil, fmt.Errorf("update %s has no valid SHA256SUMS entry", rel.Version)
	}
	return rel, nil
}

// Match only the published desktop payload, never a server asset or sidecar.
func desktopAsset(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	extension := ".zip"
	if req.Platform == "linux" {
		extension = ".AppImage"
	}
	suffix := "-" + req.Platform + "-" + req.Arch + extension
	found := -1
	for i, a := range assets {
		if strings.HasPrefix(a.Name, "serversend-desktop-") && strings.HasSuffix(a.Name, suffix) {
			if found != -1 {
				return -1
			} // Ambiguous releases fail closed.
			found = i
		}
	}
	return found
}

func configureUpdater(app *application.App) error {
	if currentVersion == "dev" {
		return nil
	}
	gh, err := github.New(github.Config{
		Repository:    updateRepository,
		ChecksumAsset: "SHA256SUMS",
		AssetMatcher:  desktopAsset,
		// A release download can exceed the provider's default 30-second timeout.
		HTTPClient: &http.Client{Timeout: 30 * time.Minute},
	})
	if err != nil {
		return err
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: currentVersion,
		Providers:      []updater.Provider{checksumProvider{gh}},
	}); err != nil {
		return err
	}

	menu := app.Menu.New()
	app.Menu.SetApplicationMenu(menu)
	appMenu := menu.AddSubmenu("ServerSend")
	var checking atomic.Bool
	appMenu.Add("Check for Updates…").OnClick(func(*application.Context) {
		if !checking.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer checking.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := app.Updater.CheckAndInstall(ctx); err != nil {
				app.Logger.Error("update", "error", err)
			}
		}()
	})
	appMenu.AddSeparator()
	appMenu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	return nil
}

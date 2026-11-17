package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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
		Window:         updater.WindowNone,
	}); err != nil {
		return err
	}

	flow := &updateFlow{engine: app.Updater, report: func(err error) { app.Logger.Error("update", "error", err) }}
	var window *application.WebviewWindow // Accessed only on the UI thread.
	var windowSequence uint64
	flow.show = func(view updateView) {
		application.InvokeAsync(func() {
			latest := flow.snapshot()
			if latest.State == "" || latest.Revision > view.Revision {
				return
			}
			if window == nil {
				windowSequence++
				window = app.Window.NewWithOptions(application.WebviewWindowOptions{
					Name: fmt.Sprintf("serversend-update-%d", windowSequence), Title: "ServerSend Update", Width: 520, Height: 520,
					HTML: updateHTML, AllowSimpleEventEmit: true,
				})
				window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
					if !flow.dismiss() {
						e.Cancel()
						return
					}
					window = nil
				})
			}
			window.Show()
			window.Focus()
			window.EmitEvent("serversend:update:state", view)
		})
	}
	for _, action := range []string{"ready", "install", "close"} {
		app.Event.On("serversend:update:"+action, func(e *application.CustomEvent) {
			application.InvokeAsync(func() {
				// Reject stale events from a previous prompt and other windows.
				if window == nil || e.Sender != window.Name() {
					return
				}
				switch action {
				case "ready":
					flow.show(flow.snapshot())
				case "install":
					go flow.install(app.Context())
				case "close":
					window.Close()
				}
			})
		})
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go flow.check(app.Context(), false)
	})
	menu := app.Menu.New()
	app.Menu.SetApplicationMenu(menu)
	appMenu := menu.AddSubmenu("ServerSend")
	appMenu.Add("Check for Updates…").OnClick(func(*application.Context) { go flow.check(app.Context(), true) })
	appMenu.AddSeparator()
	appMenu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	return nil
}

//go:embed frontend/src/update.html
var updateHTML string

type updateEngine interface {
	Check(context.Context) (*updater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
}

type updateView struct {
	Revision uint64 `json:"revision"`
	State    string `json:"state"`
	Current  string `json:"current"`
	Version  string `json:"version"`
	Notes    string `json:"notes"`
	Message  string `json:"message"`
}

// All checks and installs share one gate. The offered release remains the
// updater's pending release until consent: no second lookup can change it.
type updateFlow struct {
	engine updateEngine
	busy   atomic.Bool
	mu     sync.Mutex
	view   updateView
	show   func(updateView)
	report func(error)
}

func (f *updateFlow) snapshot() updateView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

func (f *updateFlow) display(view updateView) {
	f.mu.Lock()
	view.Revision = f.view.Revision + 1
	f.view = view
	f.mu.Unlock()
	f.show(view)
}

func (f *updateFlow) check(parent context.Context, manual bool) {
	if !f.busy.CompareAndSwap(false, true) {
		return
	}
	defer f.busy.Store(false)
	// Keep an outstanding offer stable, including across manual menu clicks.
	if view := f.snapshot(); view.Version != "" {
		f.show(view)
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	rel, err := f.engine.Check(ctx)
	if err != nil {
		f.report(err)
		if manual {
			f.display(updateView{State: "error", Current: currentVersion, Message: "Could not check for updates. Check your connection and try again from the ServerSend menu."})
		}
		return
	}
	if rel == nil {
		if manual {
			f.display(updateView{State: "current", Current: currentVersion})
		}
		return
	}
	f.display(updateView{State: "available", Current: currentVersion, Version: rel.Version, Notes: rel.Notes})
}

func (f *updateFlow) dismiss() bool {
	if !f.busy.CompareAndSwap(false, true) {
		return false
	}
	defer f.busy.Store(false)
	f.mu.Lock()
	f.view = updateView{Revision: f.view.Revision + 1}
	f.mu.Unlock()
	return true
}

func (f *updateFlow) install(parent context.Context) {
	if !f.busy.CompareAndSwap(false, true) {
		return
	}
	defer f.busy.Store(false)
	view := f.snapshot()
	if view.Version == "" || (view.State != "available" && view.State != "error") {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	view.State = "installing"
	view.Message = "Downloading and verifying the update…"
	f.display(view)
	err := f.engine.DownloadAndInstall(ctx)
	if err == nil {
		view.Message = "Restarting to apply the update…"
		f.display(view)
		err = f.engine.Restart(ctx)
	}
	if err != nil {
		f.report(err)
		view.State = "error"
		view.Message = "The update could not be applied. " + err.Error() + " Keep the app in a writable folder and try again."
		f.display(view)
	}
}

package main

import (
	_ "embed"
	"slices"
	"sync"

	"github.com/chironjit/serversend/apps/desktop/internal/transfer"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed build/trayicon.png
var trayIcon []byte

// backgroundArg starts the app hidden in the tray; launch at login passes it.
const backgroundArg = "--background"

const autostartID = "serversend"

// BackgroundService owns the window/tray lifecycle. Closing the window keeps
// transfers running; only Quit (tray, app menu or this service) exits.
type BackgroundService struct {
	app      *application.App
	manager  *transfer.Manager
	window   *application.WebviewWindow
	mu       sync.Mutex
	pause    *application.MenuItem
	login    *application.MenuItem
	trayMenu *application.Menu
}

func startedInBackground(args []string) bool { return slices.Contains(args, backgroundArg) }

// attach wires the main window and tray. Call once, before app.Run.
func (b *BackgroundService) attach(window *application.WebviewWindow) {
	b.window = window
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
	})
	b.app.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) {
		if err := b.manager.Wake(); err != nil {
			b.app.Logger.Warn("wake recovery", "error", err)
		}
	})
	b.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { b.ShowWindow() })

	menu := b.app.NewMenu()
	menu.Add("Open ServerSend").OnClick(func(*application.Context) { b.ShowWindow() })
	menu.AddSeparator()
	b.pause = menu.AddCheckbox("Pause all transfers", b.manager.Paused()).OnClick(func(ctx *application.Context) {
		_ = b.SetPaused(ctx.ClickedMenuItem().Checked())
	})
	b.login = menu.AddCheckbox("Launch at login", b.LaunchAtLogin()).OnClick(func(ctx *application.Context) {
		_ = b.SetLaunchAtLogin(ctx.ClickedMenuItem().Checked())
	})
	menu.AddSeparator()
	menu.Add("Quit ServerSend").OnClick(func(*application.Context) { b.app.Quit() })
	b.trayMenu = menu
	tray := b.app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("ServerSend")
	tray.SetMenu(menu)
	tray.OnClick(b.ShowWindow)
}

// ShowWindow brings the main window back from the tray.
func (b *BackgroundService) ShowWindow() {
	if b.window == nil {
		return
	}
	application.InvokeAsync(func() {
		b.window.Show()
		b.window.UnMinimise()
		b.window.Focus()
	})
}

// Quit stops transfers at their checkpoints and exits the application.
func (b *BackgroundService) Quit() { b.app.Quit() }

func (b *BackgroundService) Paused() bool { return b.manager.Paused() }

// SetPaused holds or releases every transfer, persistently.
func (b *BackgroundService) SetPaused(paused bool) error {
	err := b.manager.SetPaused(paused)
	b.sync(b.pause, b.manager.Paused())
	return err
}

func (b *BackgroundService) LaunchAtLogin() bool {
	enabled, err := b.app.Autostart.IsEnabled()
	return err == nil && enabled
}

// SetLaunchAtLogin registers the app to start hidden in the tray at login.
func (b *BackgroundService) SetLaunchAtLogin(enabled bool) error {
	var err error
	if enabled {
		err = b.app.Autostart.EnableWithOptions(application.AutostartOptions{Identifier: autostartID, Arguments: []string{backgroundArg}})
	} else {
		err = b.app.Autostart.Disable()
	}
	b.sync(b.login, b.LaunchAtLogin())
	return err
}

// sync keeps a tray checkbox consistent with the durable state after a change
// from either the tray or the window.
func (b *BackgroundService) sync(item *application.MenuItem, checked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item == nil {
		return
	}
	item.SetChecked(checked)
	b.trayMenu.Update()
}

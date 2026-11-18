package main

import (
	"context"
	"embed"
	"log"
	"os"
	"time"

	"github.com/chironjit/serversend/apps/desktop/internal/transfer"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[[]transfer.Item](TransferEvent)
}

func main() {
	// Transfers stop when this context ends: on explicit quit, not when the
	// window closes to the tray.
	ctx, stop := context.WithCancel(context.Background())
	transfers := NewTransferService(ctx)
	background := &BackgroundService{manager: transfers.manager}
	app := application.New(application.Options{
		Name: "desktop",
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.serversend.desktop",
			// Relaunching shows the hidden window, including where no tray is shown.
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { background.ShowWindow() },
		},
		Description: "ServerSend desktop client",
		Services: []application.Service{
			application.NewService(transfers),
			application.NewService(background),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		// Workers record their checkpoints and close the queue before exit.
		OnShutdown: func() {
			stop()
			select {
			case <-transfers.manager.Stopped():
			case <-time.After(10 * time.Second):
			}
		},
	})
	background.app = app

	if err := configureUpdater(app); err != nil {
		log.Fatalf("configure updater: %v", err)
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "ServerSend",
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1000,
		Height: 618,
		Hidden: startedInBackground(os.Args[1:]),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})
	background.attach(window)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

type flowEngine struct {
	release                           *updater.Release
	checkErr, downloadErr, restartErr error
	checks, downloads, restarts       int
	downloading                       func()
}

func (e *flowEngine) Check(context.Context) (*updater.Release, error) {
	e.checks++
	return e.release, e.checkErr
}
func (e *flowEngine) DownloadAndInstall(context.Context) error {
	e.downloads++
	if e.downloading != nil {
		e.downloading()
	}
	return e.downloadErr
}
func (e *flowEngine) Restart(context.Context) error { e.restarts++; return e.restartErr }

func TestStartupUpdateConsent(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			engine := &flowEngine{release: &updater.Release{Version: "2.0.0", Notes: "New release", Artifact: updater.Artifact{Platform: platform}}}
			var views []updateView
			flow := &updateFlow{engine: engine, show: func(v updateView) { views = append(views, v) }, report: func(error) {}}
			flow.check(context.Background(), false)
			if engine.downloads != 0 || engine.restarts != 0 || len(views) != 1 || views[0].State != "available" {
				t.Fatal("startup did not wait for consent")
			}
			flow.check(context.Background(), true)
			if engine.checks != 1 {
				t.Fatal("manual check changed an outstanding offer")
			}
			if !flow.dismiss() {
				t.Fatal("cannot postpone update")
			}
			flow.install(context.Background())
			if engine.downloads != 0 {
				t.Fatal("dismissed offer could install")
			}
			flow.check(context.Background(), false)
			engine.downloading = func() {
				flow.check(context.Background(), true)
				flow.install(context.Background())
				if flow.dismiss() {
					t.Fatal("dismissed an install in progress")
				}
			}
			flow.install(context.Background())
			if engine.downloads != 1 || engine.restarts != 1 || engine.checks != 2 {
				t.Fatalf("flow: %+v", engine)
			}
		})
	}
}

func TestUpdateFailuresAndNoUpdate(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, checkErr := range []error{nil, errors.New("offline")} {
			engine := &flowEngine{checkErr: checkErr}
			shows := 0
			flow := &updateFlow{engine: engine, show: func(updateView) { shows++ }, report: func(error) {}}
			flow.check(context.Background(), manual)
			if (shows != 0) != manual {
				t.Fatalf("manual=%v, shows=%d", manual, shows)
			}
			flow.install(context.Background())
			if engine.downloads != 0 {
				t.Fatal("installed without an offer")
			}
		}
	}
	for _, stage := range []string{"download", "restart"} {
		engine := &flowEngine{release: &updater.Release{Version: "2.0.0"}}
		if stage == "download" {
			engine.downloadErr = errors.New("checksum mismatch")
		} else {
			engine.restartErr = errors.New("read-only destination")
		}
		flow := &updateFlow{engine: engine, show: func(updateView) {}, report: func(error) {}}
		flow.check(context.Background(), false)
		flow.install(context.Background())
		if flow.snapshot().State != "error" {
			t.Fatal("failure was hidden")
		}
		if stage == "download" && engine.restarts != 0 {
			t.Fatal("restarted after verification failed")
		}
		if !flow.dismiss() {
			t.Fatal("failure prevented dismissal")
		}
	}
}

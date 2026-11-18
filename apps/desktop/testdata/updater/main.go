// Headless native fixture exercising the real Wails helper, not a mock swap.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

var version = "old"
var report string

func init() { updater.HandleHelperMode() }
func main() {
	if len(os.Args) == 1 {
		path, err := base64.RawURLEncoding.DecodeString(report)
		must(err)
		// Publish the completed report so the polling test can only read a full version.
		pending := string(path) + ".tmp"
		must(os.WriteFile(pending, []byte(version), 0600))
		must(os.Rename(pending, string(path)))
		return
	}
	p := &provider{path: os.Args[1]}
	u := updater.New(host{})
	must(u.Init(updater.Config{CurrentVersion: "1.0.0", Providers: []updater.Provider{p}, Window: updater.WindowNone}))
	_, err := u.Check(context.Background())
	must(err)
	must(u.DownloadAndInstall(context.Background()))
	must(u.Restart(context.Background()))
	select {} // Host.Quit exits; never allow the parent to run normal startup.
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

type host struct{}

func (host) Emit(string, ...any) bool                              { return false }
func (host) OnEvent(string, func(any)) func()                      { return func() {} }
func (host) OpenWindow(updater.WindowOptions) updater.WindowHandle { panic("unexpected UI") }
func (host) Quit()                                                 { os.Exit(0) }

type provider struct{ path string }

func (*provider) Name() string { return "fixture" }
func (p *provider) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return &updater.Release{Version: "2.0.0", Artifact: updater.Artifact{Filename: filepath.Base(p.path)}, Verification: &updater.Verification{DigestAlgo: "sha256", Digest: sum[:]}}, nil
}
func (p *provider) Download(_ context.Context, _ *updater.Release, w io.Writer, progress func(int64, int64)) error {
	f, err := os.Open(p.path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

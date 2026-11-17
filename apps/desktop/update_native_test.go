package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNativeUpdateSwapAndRelaunch(t *testing.T) {
	directory := t.TempDir()
	report := filepath.Join(directory, "relaunched-version")
	binary := "serversend"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	old := filepath.Join(directory, "installed", binary)
	next := filepath.Join(directory, "release", binary)
	for _, build := range []struct{ path, version string }{{old, "old"}, {next, "new"}} {
		if err := os.MkdirAll(filepath.Dir(build.path), 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "build", "-mod=readonly", "-ldflags=-X main.version="+build.version+" -X main.report="+base64.RawURLEncoding.EncodeToString([]byte(report)), "-o", build.path, "./testdata/updater")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	if runtime.GOOS == "linux" && os.Getenv("SERVERSEND_TEST_APPIMAGE") == "1" {
		for _, path := range []*string{&old, &next} {
			image := *path + ".AppImage"
			cmd := exec.Command("bash", "build/linux/appimage/build.sh")
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(), "APP_NAME=serversend", "APP_BINARY="+*path, "ICON_PATH="+filepath.Join(cwd, "build", "appicon.png"), "DESKTOP_FILE="+filepath.Join(cwd, "build", "linux", "desktop.desktop"), "APPIMAGE_OUTPUT="+image)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("AppImage packaging: %v\n%s", err, out)
			}
			*path = image
		}
	}
	target := old
	artifact := next
	// macOS replaces the enclosing bundle; Windows extracts a single EXE.
	if runtime.GOOS == "darwin" {
		old = makeUpdateBundle(t, filepath.Join(directory, "installed"), old)
		next = makeUpdateBundle(t, filepath.Join(directory, "release"), next)
		target = filepath.Join(old, "Contents", "MacOS", binary)
	}
	if runtime.GOOS != "linux" {
		artifact = filepath.Join(directory, "update.zip")
		zipUpdate(t, next, artifact)
	}
	cmd := exec.Command(target, artifact)
	cmd.Env = append(os.Environ(), "APPIMAGE_EXTRACT_AND_RUN=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(report); err == nil {
			if string(content) != "new" {
				t.Fatalf("relaunched old version: %q", content)
			}
			newBytes, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			releaseBinary := next
			if runtime.GOOS == "darwin" {
				releaseBinary = filepath.Join(next, "Contents", "MacOS", binary)
			}
			want, err := os.ReadFile(releaseBinary)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(newBytes, want) {
				t.Fatal("installed bytes differ from verified release")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("helper did not relaunch the new version within 30 seconds; inspect wails-update-*.log in the runner temp directory")
}

func makeUpdateBundle(t *testing.T, parent, binary string) string {
	t.Helper()
	bundle := filepath.Join(parent, "ServerSend.app")
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "Resources"), 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(binary)
	if err := os.Rename(binary, filepath.Join(macos, name)); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>%s</string><key>CFBundleIdentifier</key><string>com.serversend.updater-test</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`, name)
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Resources", "fixture.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--deep", "--sign", "-", bundle).CombinedOutput(); err != nil {
		t.Fatalf("sign fixture: %v\n%s", err, out)
	}
	return bundle
}

func zipUpdate(t *testing.T, source, target string) {
	t.Helper()
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name, err := filepath.Rel(filepath.Dir(source), path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		dst, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = dst.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

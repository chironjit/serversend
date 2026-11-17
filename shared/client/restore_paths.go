package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/chironjit/serversend/shared/source"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ValidateRestoreName applies the common safe subset of Windows, macOS and
// Linux names. A catalog name is one component, never a relative path.
func ValidateRestoreName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || len(name) > 255 || len(utf16.Encode([]rune(name))) > 255 {
		return errors.New("restore name is empty, invalid or too long")
	}
	if strings.HasPrefix(strings.ToLower(name), ".serversend-") {
		return errors.New("restore name uses the reserved transfer-checkpoint namespace")
	}
	if strings.TrimRight(name, ". ") != name || strings.ContainsAny(name, `<>:"/\|?*`) {
		return fmt.Errorf("%q cannot be restored safely on supported platforms", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("restore name contains control characters")
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return fmt.Errorf("%q is a reserved device name", name)
	}
	if len([]rune(stem)) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", []rune(stem)[3]) {
		return fmt.Errorf("%q is a reserved device name", name)
	}
	return nil
}

// ValidateRestorePath refuses ambiguous components before filesystem work.
func ValidateRestorePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("restore destination must be an absolute clean path")
	}
	rest := strings.TrimPrefix(path, filepath.VolumeName(path))
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		if err := ValidateRestoreName(part); err != nil {
			return err
		}
	}
	limit := 4095
	if runtime.GOOS == "windows" {
		limit = 240
	}
	if len(path) > limit {
		return errors.New("restore destination exceeds the platform path length limit")
	}
	return nil
}

// CanonicalRestorePath is a conservative collision key, including on case-
// sensitive filesystems. Persist it under the restore batch's unique constraint.
func CanonicalRestorePath(path string) (string, error) {
	if err := ValidateRestorePath(path); err != nil {
		return "", err
	}
	return cases.Fold().String(norm.NFC.String(path)), nil
}

// openRestoreRoot first checks every ancestor without following links, then
// binds the Root handle to that same inode. Later path replacements cannot
// redirect publication to another directory.
func openRestoreRoot(path string) (*os.Root, error) {
	f, err := source.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	checked, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !checked.IsDir() {
		return nil, errors.New("restore parent is not a directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(checked, actual) {
		root.Close()
		return nil, errors.New("restore parent changed while opening")
	}
	return root, nil
}

// EnsureRestoreDirectory creates one target directory. Its parent must already
// exist; recursively discovered directories are scheduled in parent-first order.
func EnsureRestoreDirectory(path string) error {
	if err := ValidateRestorePath(path); err != nil {
		return err
	}
	root, err := openRestoreRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(path)
	if err := rejectRestoreAlias(root, name); err != nil {
		return err
	}
	if err := root.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore directory is a link or conflicts with a file")
	}
	return syncRestoreDirectory(root)
}

// rejectRestoreAlias catches existing local spellings that would collide on a
// different supported filesystem. Enumeration stays bounded even in huge dirs.
func rejectRestoreAlias(root *os.Root, name string) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	key := cases.Fold().String(norm.NFC.String(name))
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			if entry.Name() != name && cases.Fold().String(norm.NFC.String(entry.Name())) == key {
				return fmt.Errorf("restore name %q collides with existing local name %q", name, entry.Name())
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

var downloadLocks = struct {
	sync.Mutex
	byPath map[string]*downloadLock
}{byPath: map[string]*downloadLock{}}

type downloadLock struct {
	gate chan struct{}
	refs int
}

func lockDownloadDestination(ctx context.Context, destination string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := CanonicalRestorePath(destination)
	if err != nil {
		return nil, err
	}
	downloadLocks.Lock()
	lock := downloadLocks.byPath[key]
	if lock == nil {
		lock = &downloadLock{gate: make(chan struct{}, 1)}
		downloadLocks.byPath[key] = lock
	}
	lock.refs++
	downloadLocks.Unlock()
	releaseReference := func() {
		downloadLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(downloadLocks.byPath, key)
		}
		downloadLocks.Unlock()
	}
	select {
	case lock.gate <- struct{}{}:
		return func() { <-lock.gate; releaseReference() }, nil
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	}
}

func syncRestoreDirectory(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

package twofactor

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const ProtectionKeyFile = "totp.key"

const (
	protectionKeyMagic     = "SSTOTP\x00\x01"
	protectionKeyFileBytes = len(protectionKeyMagic) + 32 + sha256.Size
)

// LoadProtectionKey opens a private, versioned key file in an existing absolute
// config directory. Creation must be explicitly permitted only when no retained
// enrollment secrets depend on a missing key. The caller must check that database
// invariant; losing an existing key is a restore operation, never key rotation.
//
// New keys contain 32 cryptographically random bytes and are published only after
// file flush through an exclusive hard link, then a directory flush. Concurrent
// initializers converge on the winning key. Corrupt or unsafe files are never
// repaired or replaced. The checksum detects accidental corruption; file privacy
// and trusted parent directories establish trust against local modification.
// POSIX permissions cannot establish Windows ACL privacy: operators must provision
// ACLs, and unsupported directory sync returns an error rather than claiming
// durable initialization. Windows filesystem qualification remains separate.
func LoadProtectionKey(configDir string, createIfMissing bool) ([]byte, error) {
	return loadProtectionKey(configDir, createIfMissing, rand.Reader, nativeProtectionKeyStorage())
}

func loadProtectionKey(configDir string, createIfMissing bool, random io.Reader, storage protectionKeyStorage) ([]byte, error) {
	if !filepath.IsAbs(configDir) {
		return nil, errors.New("TOTP protection key config directory must be absolute")
	}
	// Cleaning also prevents a trailing separator from making Lstat follow a
	// symlink at the config directory itself.
	configDir = filepath.Clean(configDir)
	info, err := os.Lstat(configDir)
	if err != nil {
		return nil, fmt.Errorf("inspect TOTP protection key directory: %w", err)
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("TOTP protection key directory must be an existing private real directory (mode 0700)")
	}
	path := filepath.Join(configDir, ProtectionKeyFile)
	key, err := readProtectionKey(path)
	if err == nil {
		// Another initializer may have published immediately before our read.
		// Every successful opener flushes the directory before using the key.
		if err := storage.syncDirectory(configDir); err != nil {
			return nil, fmt.Errorf("flush TOTP protection key directory: %w", err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) || !createIfMissing {
		return nil, err
	}
	generated := make([]byte, 32)
	if _, err := io.ReadFull(random, generated); err != nil {
		return nil, fmt.Errorf("generate TOTP protection key: %w", err)
	}
	if err := publishProtectionKey(path, generated, storage); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("persist TOTP protection key: %w", err)
	}
	// Always read back the exclusive publication, including when a concurrent
	// initializer won. Never return the losing locally generated key.
	key, err = readProtectionKey(path)
	if err != nil {
		return nil, err
	}
	if err := storage.syncDirectory(configDir); err != nil {
		return nil, fmt.Errorf("flush TOTP protection key directory: %w", err)
	}
	return key, nil
}

func readProtectionKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect TOTP protection key: %w", err)
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 || info.Size() != int64(protectionKeyFileBytes) {
		return nil, ErrProtectionKeyFile
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open TOTP protection key: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0 || opened.Size() != int64(protectionKeyFileBytes) {
		_ = file.Close()
		return nil, ErrProtectionKeyFile
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(protectionKeyFileBytes+1)))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read TOTP protection key: %w", err)
	}
	return decodeProtectionKey(data)
}

func encodeProtectionKey(key []byte) []byte {
	encoded := append([]byte(protectionKeyMagic), key...)
	checksum := sha256.Sum256(encoded)
	return append(encoded, checksum[:]...)
}

func decodeProtectionKey(encoded []byte) ([]byte, error) {
	if len(encoded) != protectionKeyFileBytes || string(encoded[:len(protectionKeyMagic)]) != protectionKeyMagic {
		return nil, ErrProtectionKeyFile
	}
	payload := encoded[:len(protectionKeyMagic)+32]
	checksum := sha256.Sum256(payload)
	if !bytes.Equal(checksum[:], encoded[len(payload):]) {
		return nil, ErrProtectionKeyFile
	}
	return bytes.Clone(payload[len(protectionKeyMagic):]), nil
}

// These narrow dependencies represent durability boundaries, so tests can inject
// disk-full, file-flush and directory-flush failures against real temporary files.
type protectionKeyFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type protectionKeyStorage struct {
	createTemporary func(string) (protectionKeyFile, error)
	link            func(string, string) error
	syncDirectory   func(string) error
}

func nativeProtectionKeyStorage() protectionKeyStorage {
	return protectionKeyStorage{
		createTemporary: func(directory string) (protectionKeyFile, error) {
			return os.CreateTemp(directory, ".totp-key-*") // mode 0600
		},
		link: os.Link,
		syncDirectory: func(directory string) error {
			file, err := os.Open(directory)
			if err != nil {
				return err
			}
			return errors.Join(file.Sync(), file.Close())
		},
	}
}

func publishProtectionKey(path string, key []byte, storage protectionKeyStorage) error {
	file, err := storage.createTemporary(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encoded := encodeProtectionKey(key)
	n, writeErr := file.Write(encoded)
	if writeErr == nil && n != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := storage.link(file.Name(), path); err != nil {
		return err
	}
	if err := os.Remove(file.Name()); err != nil {
		return err // The published key remains; never undo a successful link.
	}
	return storage.syncDirectory(filepath.Dir(path))
}

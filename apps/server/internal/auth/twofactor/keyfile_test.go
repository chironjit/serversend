package twofactor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/chironjit/serversend/shared/testutil"
)

func protectionKeyDirectory(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("key-file directory flush and Windows ACL privacy need native platform qualification")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestProtectionKeyPersistenceAndRestart(t *testing.T) {
	directory := protectionKeyDirectory(t)
	path := filepath.Join(directory, ProtectionKeyFile)
	if key, err := LoadProtectionKey(directory, false); !errors.Is(err, os.ErrNotExist) || key != nil {
		t.Fatal("missing key must require explicit creation permission")
	}
	first, err := LoadProtectionKey(directory, true)
	if err != nil || len(first) != 32 {
		t.Fatal("could not initialize protection key")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != int64(protectionKeyFileBytes) {
		t.Fatal("key file must have private permissions and a bounded versioned format")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewSecretProtector(first)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := protector.Seal(protectionScope, protectionSecret)
	if err != nil {
		t.Fatal(err)
	}
	for _, allowCreation := range []bool{false, true} {
		loaded, err := LoadProtectionKey(directory, allowCreation)
		if err != nil || !bytes.Equal(first, loaded) {
			t.Fatal("ordinary restart replaced the retained protection key")
		}
		restarted, err := NewSecretProtector(loaded)
		if err != nil {
			t.Fatal(err)
		}
		if secret, err := restarted.Open(protectionScope, envelope); err != nil || secret != protectionSecret {
			t.Fatal("retained key could not open pre-restart enrollment")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(encoded, after) {
		t.Fatal("repeated loading mutated the key file")
	}
}

func TestProtectionKeyConcurrentInitializationConverges(t *testing.T) {
	directory := protectionKeyDirectory(t)
	keys := make(chan []byte, 32)
	failures := make(chan error, 32)
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			key, err := LoadProtectionKey(directory, true)
			if err != nil {
				failures <- err
				return
			}
			keys <- key
		})
	}
	group.Wait()
	close(keys)
	close(failures)
	for err := range failures {
		t.Errorf("concurrent key initialization: %v", err)
	}
	retained, err := LoadProtectionKey(directory, false)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for key := range keys {
		count++
		if !bytes.Equal(retained, key) {
			t.Fatal("racing initializer returned a losing unpublished key")
		}
	}
	if count != 32 {
		t.Fatal("some initializers did not complete")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || files[0].Name() != ProtectionKeyFile {
		t.Fatal("initialization left temporary key material")
	}
}

func TestProtectionKeyRejectsUnsafePathsAndCorruptMaterial(t *testing.T) {
	for _, scenario := range []string{
		"relative", "missing directory", "public directory", "config file", "config symlink", "key directory", "key symlink", "public key", "empty", "truncated", "oversized", "wrong version", "modified key", "modified checksum",
	} {
		t.Run(scenario, func(t *testing.T) {
			directory := protectionKeyDirectory(t)
			path := filepath.Join(directory, ProtectionKeyFile)
			material := encodeProtectionKey(bytes.Repeat([]byte{0x91}, 32))
			writeMaterial := false
			switch scenario {
			case "relative":
				directory = "relative-config"
			case "missing directory":
				directory = filepath.Join(directory, "missing")
			case "public directory":
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			case "config file":
				directory = filepath.Join(directory, "config")
				if err := os.WriteFile(directory, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "config symlink":
				link := filepath.Join(directory, "alias")
				if err := os.Symlink(directory, link); err != nil {
					t.Fatal(err)
				}
				directory = link + string(filepath.Separator)
			case "key directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "key symlink":
				target := filepath.Join(directory, "target")
				if err := os.WriteFile(target, material, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "public key":
				if err := os.WriteFile(path, material, 0o644); err != nil {
					t.Fatal(err)
				}
			case "empty":
				material, writeMaterial = nil, true
			case "truncated":
				material, writeMaterial = material[:len(material)-1], true
			case "oversized":
				material, writeMaterial = append(material, 0), true
			case "wrong version":
				material[len(protectionKeyMagic)-1] = 0xff
				writeMaterial = true
			case "modified key":
				material[len(protectionKeyMagic)] ^= 1
				writeMaterial = true
			case "modified checksum":
				material[len(material)-1] ^= 1
				writeMaterial = true
			}
			if writeMaterial {
				if err := os.WriteFile(path, material, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if key, err := LoadProtectionKey(directory, true); err == nil || key != nil {
				t.Fatal("unsafe or corrupt protection key configuration accepted")
			}
			if writeMaterial || scenario == "public key" {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, material) {
					t.Fatal("invalid existing key was silently replaced")
				}
			}
		})
	}
}

func TestProtectionKeyRejectsReadOnlyInitialization(t *testing.T) {
	directory := protectionKeyDirectory(t)
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX directory write permission")
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	if key, err := LoadProtectionKey(directory, true); err == nil || key != nil {
		t.Fatal("read-only directory accepted for initialization")
	}
}

type injectedKeyFile struct{ *testutil.File }

func (f *injectedKeyFile) Name() string { return f.File.File.Name() }

type shortKeyFile struct{ *os.File }

func (f *shortKeyFile) Write(data []byte) (int, error) {
	return (&testutil.ShortWriter{Writer: f.File, Remaining: 17}).Write(data)
}

func TestProtectionKeyPrepublicationFailuresLeaveNoKey(t *testing.T) {
	for _, stage := range []string{"entropy", "short entropy", "create", "write", "short write", "sync", "close", "link"} {
		t.Run(stage, func(t *testing.T) {
			directory := protectionKeyDirectory(t)
			storage := nativeProtectionKeyStorage()
			var random io.Reader = bytes.NewReader(bytes.Repeat([]byte{0x91}, 32))
			faults := &testutil.Faults{}
			switch stage {
			case "entropy":
				random = bytes.NewReader(nil)
			case "short entropy":
				random = bytes.NewReader(make([]byte, 31))
			case "create":
				storage.createTemporary = func(string) (protectionKeyFile, error) { return nil, testutil.ErrInterrupted }
			case "write", "sync", "close":
				faults.Arm(stage, 1, testutil.ErrInterrupted)
				storage.createTemporary = func(directory string) (protectionKeyFile, error) {
					file, err := os.CreateTemp(directory, ".totp-key-*")
					if err != nil {
						return nil, err
					}
					return &injectedKeyFile{File: &testutil.File{File: file, Faults: faults}}, nil
				}
			case "short write":
				storage.createTemporary = func(directory string) (protectionKeyFile, error) {
					file, err := os.CreateTemp(directory, ".totp-key-*")
					if err != nil {
						return nil, err
					}
					return &shortKeyFile{File: file}, nil
				}
			case "link":
				storage.link = func(string, string) error { return testutil.ErrInterrupted }
			}
			if key, err := loadProtectionKey(directory, true, random, storage); err == nil || key != nil {
				t.Fatal("failed persistence returned a usable protection key")
			}
			files, err := os.ReadDir(directory)
			if err != nil || len(files) != 0 {
				t.Fatal("prepublication failure retained key material")
			}
			if _, err := LoadProtectionKey(directory, true); err != nil {
				t.Fatal("initialization could not recover after unpublished failure")
			}
		})
	}
}

func TestProtectionKeyDirectoryFlushFailureRetainsPublishedKey(t *testing.T) {
	directory := protectionKeyDirectory(t)
	storage := nativeProtectionKeyStorage()
	storage.syncDirectory = func(string) error { return testutil.ErrInterrupted }
	if key, err := loadProtectionKey(directory, true, bytes.NewReader(bytes.Repeat([]byte{0x91}, 32)), storage); !errors.Is(err, testutil.ErrInterrupted) || key != nil {
		t.Fatal("directory flush failure returned success or a usable key")
	}
	before, err := os.ReadFile(filepath.Join(directory, ProtectionKeyFile))
	if err != nil {
		t.Fatal("flush failure must retain its already published key")
	}
	retained, err := decodeProtectionKey(before)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := LoadProtectionKey(directory, false)
	if err != nil || !bytes.Equal(retained, restarted) {
		t.Fatal("restart replaced a key published before directory flush failure")
	}
	after, err := os.ReadFile(filepath.Join(directory, ProtectionKeyFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("restart mutated a key after uncertain publication")
	}
}

func TestProtectionKeyIgnoresUnpublishedCrashTemporary(t *testing.T) {
	directory := protectionKeyDirectory(t)
	staged := bytes.Repeat([]byte{0xa4}, 32)
	if err := os.WriteFile(filepath.Join(directory, ".totp-key-interrupted"), encodeProtectionKey(staged), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unpublished material was never returned to a successful caller.
	if key, err := LoadProtectionKey(directory, false); !errors.Is(err, os.ErrNotExist) || key != nil {
		t.Fatal("unpublished crash temporary was treated as a retained key")
	}
	created, err := LoadProtectionKey(directory, true)
	if err != nil || bytes.Equal(created, staged) {
		t.Fatal("fresh initialization adopted unpublished crash material")
	}
}

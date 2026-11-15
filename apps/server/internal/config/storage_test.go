package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func storageConfig(t *testing.T) Config {
	t.Helper()
	c := Defaults()
	root := t.TempDir()
	c.ConfigDir = filepath.Join(root, "config")
	c.DataDir = filepath.Join(root, "data")
	c.StagingDir = filepath.Join(root, "staging")
	for _, dir := range []string{c.ConfigDir, c.DataDir, c.StagingDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestStorageProbeCleansUp(t *testing.T) {
	c := storageConfig(t)
	if err := c.CheckStorage(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{c.ConfigDir, c.DataDir, c.StagingDir} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("probe left files: %v error=%v", entries, err)
		}
	}
}

func TestStorageRejectsMissingUnsafeAndAliasedDirectories(t *testing.T) {
	for _, scenario := range []string{"missing", "file", "same alias", "nested alias", "public config", "read only"} {
		t.Run(scenario, func(t *testing.T) {
			c := storageConfig(t)
			switch scenario {
			case "missing":
				c.DataDir = filepath.Join(t.TempDir(), "missing")
			case "file":
				c.DataDir = filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(c.DataDir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "same alias", "nested alias":
				target := c.DataDir
				if scenario == "nested alias" {
					target = filepath.Join(target, "child")
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				c.StagingDir = filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(target, c.StagingDir); err != nil {
					t.Fatal(err)
				}
			case "public config":
				if err := os.Chmod(c.ConfigDir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "read only":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses Unix write permissions")
				}
				if err := os.Chmod(c.DataDir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(c.DataDir, 0o700) })
			}
			if err := c.CheckStorage(); err == nil {
				t.Fatal("unsafe storage accepted")
			} else if strings.Contains(scenario, "alias") && !strings.Contains(err.Error(), "overlap") {
				t.Fatalf("alias not detected: %v", err)
			}
		})
	}
}

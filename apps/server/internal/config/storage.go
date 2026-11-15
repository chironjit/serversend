package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CheckStorage checks existing deployment directories, including aliases from
// symlinks and bind mounts. It does not create missing mounts, prove that a path
// is a mount, or qualify network filesystem locking and durability semantics.
func (c Config) CheckStorage() error {
	if err := c.Validate(); err != nil {
		return err
	}
	type directory struct {
		name string
		path string
		info os.FileInfo
	}
	var checked []directory
	for _, dir := range []directory{
		{name: "config", path: c.ConfigDir},
		{name: "data", path: c.DataDir},
		{name: "staging", path: c.StagingDir},
	} {
		resolved, err := filepath.EvalSymlinks(dir.path)
		if err != nil {
			return fmt.Errorf("%s directory must already exist: %w", dir.name, err)
		}
		dir.path = resolved
		dir.info, err = os.Stat(resolved)
		if err != nil || !dir.info.IsDir() {
			return fmt.Errorf("%s path must be a directory", dir.name)
		}
		if dir.name == "config" && dir.info.Mode().Perm()&0o077 != 0 {
			return errors.New("config directory must be private (mode 0700)")
		}
		for _, previous := range checked {
			for _, pair := range [][2]directory{{dir, previous}, {previous, dir}} {
				// Compare every ancestor by inode as well as canonical paths: a
				// bind mount can alias another directory without a symlink.
				for parent := pair[0].path; ; parent = filepath.Dir(parent) {
					info, err := os.Stat(parent)
					if err != nil {
						return fmt.Errorf("inspect %s ancestors: %w", pair[0].name, err)
					}
					if os.SameFile(info, pair[1].info) {
						return fmt.Errorf("%s and %s directories overlap through an alias", dir.name, previous.name)
					}
					if filepath.Dir(parent) == parent {
						break
					}
				}
			}
		}
		checked = append(checked, dir)
	}
	for _, dir := range checked {
		if err := probeDirectory(dir.path); err != nil {
			return fmt.Errorf("%s directory must support durable writes: %w", dir.name, err)
		}
	}
	return nil
}

func probeDirectory(path string) error {
	file, err := os.CreateTemp(path, ".serversend-check-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write([]byte("ServerSend storage check\n"))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Remove(file.Name()); err != nil {
		return err
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

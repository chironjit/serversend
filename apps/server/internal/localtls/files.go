package localtls

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// All paths live under an operator-controlled private config directory. Reject
// symlinks and public key-bearing files rather than silently repairing them.
func checkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("local TLS directory must be a private real directory (mode 0700)")
	}
	return nil
}

func readFile(path string, private bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || private && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("local TLS material must be a regular file with private keys protected (mode 0600)")
	}
	if info.Size() > 64<<10 {
		return nil, errors.New("local TLS file is oversized")
	}
	return os.ReadFile(path)
}

// writeFile publishes a flushed complete PEM bundle. Exclusive publication uses
// a same-directory hard link so racing first starts cannot replace a trust root.
// A failure after publication is reported; the caller must not delete/rotate it.
func writeFile(path string, data []byte, exclusive bool) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".certificate-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if exclusive {
		err = os.Link(file.Name(), path)
	} else {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("flush local TLS directory: %w", err)
	}
	return nil
}

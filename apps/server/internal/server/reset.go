package server

import (
	"context"
	"os"
	"path/filepath"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/instancelock"
)

// ResetFiles permanently removes every uploaded file and folder, keeping
// accounts, devices, identity and certificates. The server must be stopped;
// the deployment locks enforce this. Metadata is cleared first, so an
// interrupted reset can simply be run again.
func ResetFiles(ctx context.Context, c config.Config) error {
	locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
	if err != nil {
		return err
	}
	defer locks.Close()
	if err := database.ResetFiles(ctx, c.ConfigDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.DataDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != instancelock.Filename {
			if err := os.RemoveAll(filepath.Join(c.DataDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

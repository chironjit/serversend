package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chironjit/serversend/apps/server/internal/backup"
	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

// preUpgradeBackup runs under the deployment locks before migrations. A
// pending schema migration needs either an automatic backup directory or an
// explicit opt-out. The backup name is deterministic, so a failing upgrade
// that restarts in a loop cannot fill the backup volume.
func preUpgradeBackup(ctx context.Context, c config.Config) (string, error) {
	installed, id, err := database.Installed(ctx, c.ConfigDir)
	if err != nil {
		return "", fmt.Errorf("read metadata schema: %w", err)
	}
	latest := database.LatestVersion()
	if installed == 0 || installed >= latest {
		return "", nil
	}
	switch c.UpgradeBackupDir {
	case "":
		return "", fmt.Errorf("metadata schema %d needs migration to %d: set SERVER_SEND_UPGRADE_BACKUP_DIR to a private directory outside the deployment volumes for an automatic pre-upgrade backup, or to %q after taking your own stopped-server backup", installed, latest, config.SkipUpgradeBackup)
	case config.SkipUpgradeBackup:
		return fmt.Sprintf("Migrating metadata schema %d to %d without an automatic backup (SERVER_SEND_UPGRADE_BACKUP_DIR=none)", installed, latest), nil
	}
	destination := filepath.Join(c.UpgradeBackupDir, fmt.Sprintf("serversend-%s-schema-%d-to-%d", id, installed, latest))
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("pre-upgrade backup %s already exists: an earlier upgrade attempt failed or was rolled back; check it, move it aside and restart", destination)
	}
	if err := backup.CreateLocked(ctx, c, destination); err != nil {
		return "", fmt.Errorf("pre-upgrade backup: %w", err)
	}
	return fmt.Sprintf("Pre-upgrade backup of metadata schema %d written to %s before migrating to %d", installed, destination, latest), nil
}

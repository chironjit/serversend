package transfer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
)

type RestoreTask struct {
	Kind  string
	Path  string
	Entry api.CatalogEntry
}

type RestorePage struct {
	Tasks      []RestoreTask
	NextCursor string
	Revision   protocol.Revision
}

func CanonicalRestorePath(path string) (string, error) { return client.CanonicalRestorePath(path) }
func ValidateRestorePath(path string) error            { return client.ValidateRestorePath(path) }

// PlanRestoreDirectory discovers one bounded page. The caller persists every
// returned task and the cursor in a single transaction before requesting the
// next page. A unique batch/canonical-path claim detects collisions across
// pages, directory selections and restarts.
func PlanRestoreDirectory(ctx context.Context, c *client.Client, entry api.CatalogEntry, destination, cursor, policy string) (RestorePage, error) {
	if entry.Kind != api.Directory || entry.Standard == nil || entry.Mode != protocol.Standard || entry.Trashed {
		return RestorePage{}, errors.New("only live standard directories can be restored")
	}
	if policy != "replace" && policy != "skip" && policy != "fail" {
		return RestorePage{}, errors.New("choose replace, skip or fail for existing files")
	}
	if err := client.EnsureRestoreDirectory(destination); err != nil {
		return RestorePage{}, err
	}
	page, err := c.CatalogFolderPage(ctx, entry.ID, api.Cursor(cursor))
	if err != nil {
		return RestorePage{}, err
	}
	result := RestorePage{Tasks: make([]RestoreTask, 0, len(page.Entries)), NextCursor: string(page.NextCursor), Revision: page.CatalogRevision}
	seen := map[string]protocol.ID{}
	for _, child := range page.Entries {
		if child.Trashed {
			continue
		}
		if child.ParentID != entry.ID || child.Standard == nil || child.Mode != protocol.Standard {
			return RestorePage{}, errors.New("server returned an unexpected restore child")
		}
		if err := client.ValidateRestoreName(child.Standard.Name); err != nil {
			return RestorePage{}, err
		}
		path := filepath.Join(destination, child.Standard.Name)
		relative, err := filepath.Rel(destination, path)
		if err != nil || relative != child.Standard.Name {
			return RestorePage{}, errors.New("restore child escapes its destination")
		}
		key, err := CanonicalRestorePath(path)
		if err != nil {
			return RestorePage{}, err
		}
		if previous, exists := seen[key]; exists && previous != child.ID {
			return RestorePage{}, fmt.Errorf("restore names collide at %q", child.Standard.Name)
		}
		seen[key] = child.ID
		kind := "download"
		if child.Kind == api.Directory {
			kind = "scan_restore"
		} else if child.Kind != api.File {
			return RestorePage{}, errors.New("unsupported restore entry type")
		}
		result.Tasks = append(result.Tasks, RestoreTask{Kind: kind, Path: path, Entry: child})
	}
	return result, nil
}

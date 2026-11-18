package storage

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/shared/api"
	"io"
	"io/fs"
	"sort"
	"strings"
)

// Allocated walks in bounded batches, never follows links, and counts regular
// file allocations (including sparse files), not apparent lengths or directories.
func (s *Store) Allocated(ctx context.Context, name string) (int64, error) {
	return s.walkAllocated(ctx, name, nil)
}

// Breakdown classifies each observed regular file once, so concurrent moves
// cannot produce negative category totals through subtraction of separate scans.
func (s *Store) Breakdown(ctx context.Context) (map[string]int64, error) {
	result := map[string]int64{}
	_, err := s.walkAllocated(ctx, ".", func(name string, n int64) {
		name = strings.TrimPrefix(name, "./")
		category := "committed"
		if strings.HasPrefix(name, layout.Internal+"/") || name == ".serversend.lock" {
			category = "other"
		}
		switch {
		case strings.HasPrefix(name, layout.Versions+"/"):
			category = "retained"
		case strings.HasPrefix(name, layout.Uploads+"/"), strings.HasPrefix(name, layout.Tmp+"/"):
			category = "staged"
		case strings.HasPrefix(name, layout.Trash+"/"):
			category = "trash"
		}
		result[category] += n
	})
	return result, err
}
func (s *Store) walkAllocated(ctx context.Context, name string, visit func(string, int64)) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.Mode().IsRegular() {
		n, err := allocated(info, s.root, name)
		if err == nil && visit != nil {
			visit(name, n)
		}
		return n, err
	}
	if !info.IsDir() {
		return 0, nil
	}
	dir, err := s.root.Open(name)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	var total int64
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			n, e := s.walkAllocated(ctx, name+"/"+entry.Name(), visit)
			if e != nil {
				return 0, e
			}
			total += n
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// Capacities groups paths by device/volume identity. Each filesystem's free
// space appears exactly once even when config, staging and data share it.
func Capacities(paths map[string]string) ([]api.FilesystemCapacity, error) {
	result := []api.FilesystemCapacity{}
	for role, path := range paths {
		c, err := capacity(path)
		if err != nil {
			return nil, err
		}
		found := false
		for i := range result {
			if result[i].ID == c.ID {
				result[i].Roles = append(result[i].Roles, role)
				found = true
				break
			}
		}
		if !found {
			c.Roles = []string{role}
			result = append(result, c)
		}
	}
	for i := range result {
		sort.Strings(result[i].Roles)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

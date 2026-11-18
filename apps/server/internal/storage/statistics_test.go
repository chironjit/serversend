package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCapacitiesDeduplicateAndAllocatedSparse(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	capacities, err := Capacities(map[string]string{"data": dir, "staging": child, "config": dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(capacities) != 1 || len(capacities[0].Roles) != 3 || capacities[0].AvailableBytes > capacities[0].TotalBytes {
		t.Fatalf("capacity: %+v", capacities)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	f, err := os.Create(filepath.Join(dir, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(dir, "child", "bytes"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := store.Allocated(t.Context(), "child")
	if err != nil || n < 5 {
		t.Fatalf("allocated %d %v", n, err)
	}
	// Links outside the store must never contribute content.
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err == nil {
		if n, e := store.Allocated(t.Context(), "link"); n != 0 || e != nil {
			t.Fatalf("link: %d %v", n, e)
		}
	}
	breakdown, err := store.Breakdown(t.Context())
	if err != nil || breakdown["committed"] < n {
		t.Fatalf("breakdown: %+v %v", breakdown, err)
	}
}

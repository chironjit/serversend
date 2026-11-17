package applog

import (
	"os"
	"strings"
	"testing"
)

func TestPersistenceFilteringPagingAndRotation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Add("ok", "Deleted folder")
	s.Add("warn", "Upload blocked deletion")
	s.Add("error", "Disk failure")
	if page := s.Page("warn", "DELETION", 0); len(page) != 1 {
		t.Fatalf("filter: %+v", page)
	}
	page := s.Page("", "", 3)
	if len(page) != 2 || page[0].ID != 2 {
		t.Fatalf("cursor: %+v", page)
	}
	// Force several real rotations, then reload both retained files.
	for i := 0; i < 600; i++ {
		s.Add("info", strings.Repeat("x", 8192))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page = s.Page("", "", 0)
	if len(page) != 100 || page[0].ID != 603 {
		t.Fatalf("restart: %d %+v", len(page), page[0])
	}
	for _, path := range []string{s.path, s.path + ".1"} {
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxBytes {
			t.Fatalf("unbounded log: %v %v", info, err)
		}
	}
}

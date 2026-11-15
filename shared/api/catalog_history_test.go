package api

import (
	"bytes"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

func TestCatalogDeltaAndVersionHistoryBoundaries(t *testing.T) {
	base, err := DecodeCatalogChanges(bytes.NewReader(fixture(t, "catalog-changes")), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*CatalogChanges){
		"reverse interval":   func(p *CatalogChanges) { p.FromRevision = "9007199254740994" },
		"invalid interval":   func(p *CatalogChanges) { p.ThroughRevision = "01" },
		"missing changes":    func(p *CatalogChanges) { p.Changes = nil },
		"old revision":       func(p *CatalogChanges) { p.Changes[0].Revision = p.FromRevision },
		"future revision":    func(p *CatalogChanges) { p.Changes[0].Revision = "9007199254740994" },
		"duplicate revision": func(p *CatalogChanges) { p.Changes = append(p.Changes, p.Changes[0]) },
		"bad id":             func(p *CatalogChanges) { p.Changes[0].ID = "" },
		"missing upsert":     func(p *CatalogChanges) { p.Changes[0].Entry = nil },
		"wrong upsert":       func(p *CatalogChanges) { p.Changes[0].ID = accountID },
		"removed with data":  func(p *CatalogChanges) { p.Changes[0].Removed = true },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			p.Changes = append([]CatalogChange{}, base.Changes...)
			change(&p)
			if p.Validate(nil) == nil {
				t.Fatal("invalid delta accepted")
			}
		})
	}
	base.Changes[0].Removed = true
	base.Changes[0].Entry = nil
	if err := base.Validate(nil); err != nil {
		t.Fatal(err)
	}
	base.Changes = []CatalogChange{}
	if err := base.Validate(nil); err != nil {
		t.Fatal(err)
	}
	versions, err := DecodeVersionPage(bytes.NewReader(fixture(t, "versions")))
	if err != nil {
		t.Fatal(err)
	}
	v := versions.Versions[0]
	v.FileID = accountID
	versions.Versions[0] = v
	if versions.Validate() == nil {
		t.Fatal("foreign version accepted")
	}
	v.FileID = versions.FileID
	versions.Versions = []VersionInfo{v, v}
	if versions.Validate() == nil {
		t.Fatal("duplicate version accepted")
	}
	versions.Versions = nil
	if versions.Validate() == nil {
		t.Fatal("missing version list accepted")
	}
}

func TestDirectoryCreationAndBoundedPageContinuations(t *testing.T) {
	r, err := DecodeCreateDirectory(bytes.NewReader(fixture(t, "create-directory")), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedHead = "1"
	if r.Validate(nil) == nil {
		t.Fatal("directory creation accepted an existing head")
	}
	r.ExpectedHead = "0"
	r.Mode = protocol.Private
	r.Standard = nil
	r.Private = &protocol.PrivateEnvelope{Format: "test-only", Payload: []byte("opaque")}
	if r.Validate(nil) == nil {
		t.Fatal("unreviewed private directory accepted")
	}
	if err := r.Validate([]string{"test-only"}); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, MaxPageSize + 1} {
		if (Page{Snapshot: snapshotID, NextCursor: "YQ"}).validateCount(count) == nil {
			t.Fatal("empty/oversized continuation accepted")
		}
	}
	p := CatalogPage{Page: Page{Snapshot: snapshotID}, CatalogRevision: "0", Entries: []CatalogEntry{}}
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
	p.NextCursor = "YQ"
	if p.Validate(nil) == nil {
		t.Fatal("catalog page can loop without progress")
	}
	versions, err := DecodeVersionPage(bytes.NewReader(fixture(t, "versions")))
	if err != nil {
		t.Fatal(err)
	}
	base := versions.Versions[0]
	for name, change := range map[string]func(*VersionInfo){
		"id": func(v *VersionInfo) { v.ID = "" }, "file": func(v *VersionInfo) { v.FileID = "" }, "mode": func(v *VersionInfo) { v.Mode = "future" },
		"length": func(v *VersionInfo) { v.TransmittedBytes = -1 }, "digest": func(v *VersionInfo) { v.ManifestSHA256 = "bad" }, "time": func(v *VersionInfo) { v.CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			v := base
			change(&v)
			if v.Validate() == nil {
				t.Fatal("invalid immutable version accepted")
			}
		})
	}
}

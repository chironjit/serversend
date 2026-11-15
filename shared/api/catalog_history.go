package api

import (
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

type CatalogChange struct {
	Revision protocol.Revision `json:"revision"`
	ID       protocol.ID       `json:"id"`
	Removed  bool              `json:"removed"`
	Entry    *CatalogEntry     `json:"entry,omitempty"`
}

type CatalogChanges struct {
	Page
	FromRevision    protocol.Revision `json:"from_revision"`
	ThroughRevision protocol.Revision `json:"through_revision"`
	Changes         []CatalogChange   `json:"changes"`
}

func (p CatalogChanges) Validate(privateFormats []string) error {
	if err := p.Page.validateCount(len(p.Changes)); err != nil {
		return err
	}
	if cmp, err := protocol.CompareRevisions(p.FromRevision, p.ThroughRevision); err != nil || cmp > 0 {
		return errors.New("invalid catalog revision interval")
	}
	if p.Changes == nil || len(p.Changes) > MaxPageSize {
		return errors.New("invalid catalog change page size")
	}
	previous := p.FromRevision
	for _, change := range p.Changes {
		if err := change.ID.Validate(); err != nil {
			return err
		}
		if cmp, err := protocol.CompareRevisions(previous, change.Revision); err != nil || cmp >= 0 {
			return errors.New("catalog changes must increase in revision order")
		}
		if cmp, err := protocol.CompareRevisions(change.Revision, p.ThroughRevision); err != nil || cmp > 0 {
			return errors.New("catalog change exceeds snapshot revision")
		}
		if change.Removed {
			if change.Entry != nil {
				return errors.New("removed change cannot contain an entry")
			}
		} else {
			if change.Entry == nil || change.Entry.ID != change.ID {
				return errors.New("upsert change requires its matching entry")
			}
			if err := change.Entry.Validate(privateFormats); err != nil {
				return err
			}
		}
		previous = change.Revision
	}
	return nil
}

type VersionInfo struct {
	ID               protocol.ID          `json:"id"`
	FileID           protocol.ID          `json:"file_id"`
	Mode             protocol.StorageMode `json:"mode"`
	TransmittedBytes int64                `json:"transmitted_bytes"`
	ManifestSHA256   protocol.Digest      `json:"manifest_sha256"`
	CreatedAt        time.Time            `json:"created_at"`
	Conflicted       bool                 `json:"conflicted"`
}

func (v VersionInfo) Validate() error {
	if err := v.ID.Validate(); err != nil {
		return err
	}
	if err := v.FileID.Validate(); err != nil {
		return err
	}
	if err := v.Mode.Validate(); err != nil {
		return err
	}
	if err := v.ManifestSHA256.Validate(); err != nil {
		return err
	}
	if v.TransmittedBytes < 0 || v.CreatedAt.IsZero() {
		return errors.New("invalid version length or creation time")
	}
	return nil
}

type VersionPage struct {
	Page
	FileID   protocol.ID   `json:"file_id"`
	Versions []VersionInfo `json:"versions"`
}

func (p VersionPage) Validate() error {
	if err := p.Page.validateCount(len(p.Versions)); err != nil {
		return err
	}
	if err := p.FileID.Validate(); err != nil {
		return err
	}
	if p.Versions == nil || len(p.Versions) > MaxPageSize {
		return errors.New("invalid version page size")
	}
	seen := make(map[protocol.ID]bool, len(p.Versions))
	for _, v := range p.Versions {
		if err := v.Validate(); err != nil {
			return err
		}
		if v.FileID != p.FileID || seen[v.ID] {
			return errors.New("foreign or duplicate file version")
		}
		seen[v.ID] = true
	}
	return nil
}

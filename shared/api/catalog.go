package api

import (
	"errors"

	"github.com/chironjit/serversend/shared/protocol"
)

type EntryKind string

const (
	File      EntryKind = "file"
	Directory EntryKind = "directory"
)

type CatalogEntry struct {
	ID            protocol.ID                `json:"id"`
	ParentID      protocol.ID                `json:"parent_id,omitempty"`
	Kind          EntryKind                  `json:"kind"`
	Mode          protocol.StorageMode       `json:"mode"`
	HeadVersionID protocol.ID                `json:"head_version_id,omitempty"`
	HeadRevision  protocol.Revision          `json:"head_revision"`
	Trashed       bool                       `json:"trashed"`
	Standard      *protocol.StandardMetadata `json:"standard,omitempty"`
	Private       *protocol.PrivateEnvelope  `json:"private,omitempty"`
}

func (e CatalogEntry) Validate(privateFormats []string) error {
	if err := e.ID.Validate(); err != nil {
		return err
	}
	if e.ParentID != "" {
		if err := e.ParentID.Validate(); err != nil {
			return err
		}
		if e.ParentID == e.ID {
			return errors.New("entry cannot parent itself")
		}
	}
	if err := e.Mode.Validate(); err != nil {
		return err
	}
	if err := e.HeadRevision.Validate(); err != nil {
		return err
	}
	if e.HeadRevision == "0" {
		return errors.New("visible entries need a positive head revision")
	}
	switch e.Kind {
	case File:
		if err := e.HeadVersionID.Validate(); err != nil {
			return err
		}
	case Directory:
		if e.HeadVersionID != "" {
			return errors.New("directory cannot have a file version")
		}
	default:
		return errors.New("unknown catalog entry kind")
	}
	if e.Mode == protocol.Standard {
		if e.Standard == nil || e.Private != nil {
			return errors.New("standard entry requires only standard metadata")
		}
		return protocol.ValidateName(e.Standard.Name)
	}
	if e.Private == nil || e.Standard != nil {
		return errors.New("private entry requires only an opaque envelope")
	}
	return e.Private.Validate(privateFormats)
}

type CatalogPage struct {
	Page
	CatalogRevision protocol.Revision `json:"catalog_revision"`
	Entries         []CatalogEntry    `json:"entries"`
}

func (p CatalogPage) Validate(privateFormats []string) error {
	if err := p.Page.validateCount(len(p.Entries)); err != nil {
		return err
	}
	if err := p.CatalogRevision.Validate(); err != nil {
		return err
	}
	if p.Entries == nil || len(p.Entries) > MaxPageSize {
		return errors.New("invalid catalog page size")
	}
	seen := make(map[protocol.ID]bool, len(p.Entries))
	for _, entry := range p.Entries {
		if err := entry.Validate(privateFormats); err != nil {
			return err
		}
		if seen[entry.ID] {
			return errors.New("duplicate catalog entry")
		}
		seen[entry.ID] = true
	}
	return nil
}

type RevisionPrecondition struct {
	ExpectedHead protocol.Revision `json:"expected_head"`
}

func (p RevisionPrecondition) Validate() error { return p.ExpectedHead.Validate() }

// CheckRevision is compare-and-swap policy, not synchronization. Persistence
// must execute this test and its mutation within one transaction.
func CheckRevision(expected, current protocol.Revision) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := current.Validate(); err != nil {
		return err
	}
	if expected != current {
		return Error{Code: Conflict, Message: "This item changed; refresh before trying again"}
	}
	return nil
}

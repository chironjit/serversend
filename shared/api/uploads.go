package api

import (
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	IdempotencyKeyHeader = "Idempotency-Key"
	ChunkDigestHeader    = "ServerSend-Chunk-SHA256"
)

type CreateUploadRequest struct {
	FileID           protocol.ID          `json:"file_id"`
	VersionID        protocol.ID          `json:"version_id"`
	ParentID         protocol.ID          `json:"parent_id,omitempty"`
	Mode             protocol.StorageMode `json:"mode"`
	ExpectedHead     protocol.Revision    `json:"expected_head"`
	ChunkCount       int64                `json:"chunk_count"`
	TransmittedBytes int64                `json:"transmitted_bytes"`
	ManifestBytes    int64                `json:"manifest_bytes"`
}

func (u CreateUploadRequest) Validate(limits protocol.TransferLimits) error {
	if err := u.FileID.Validate(); err != nil {
		return err
	}
	if err := u.VersionID.Validate(); err != nil {
		return err
	}
	if u.ParentID != "" {
		if err := u.ParentID.Validate(); err != nil {
			return err
		}
	}
	if err := u.Mode.Validate(); err != nil {
		return err
	}
	if err := u.ExpectedHead.Validate(); err != nil {
		return err
	}
	if err := limits.ValidateManifestBounds(u.ManifestBytes, u.ChunkCount); err != nil {
		return err
	}
	if u.TransmittedBytes < 0 || (u.ChunkCount == 0) != (u.TransmittedBytes == 0) {
		return errors.New("inconsistent upload lengths")
	}
	// Check feasible lengths without multiplying potentially overflowing counts.
	if u.ChunkCount > 0 {
		n := u.ChunkCount - 1
		if n > (u.TransmittedBytes-1)/limits.MinChunkBytes {
			return errors.New("upload is shorter than its minimum chunk lengths")
		}
		if (u.TransmittedBytes-1)/limits.MaxChunkBytes >= u.ChunkCount {
			return errors.New("upload exceeds its maximum chunk lengths")
		}
	}
	return nil
}

type Upload struct {
	ID protocol.ID `json:"id"`
	CreateUploadRequest
	State     protocol.UploadState `json:"state"`
	ExpiresAt time.Time            `json:"expires_at"`
}

func (u Upload) Validate(limits protocol.TransferLimits) error {
	if err := u.ID.Validate(); err != nil {
		return err
	}
	if err := u.CreateUploadRequest.Validate(limits); err != nil {
		return err
	}
	if err := u.State.Validate(); err != nil {
		return err
	}
	if u.ExpiresAt.IsZero() {
		return errors.New("upload expiry is required")
	}
	return nil
}

// ChunkRange is half-open [start,end). Missing pages describe a stable
// snapshot. Newly acknowledged chunks may still occur in an older snapshot.
type ChunkRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type MissingChunks struct {
	Page
	UploadID   protocol.ID  `json:"upload_id"`
	ChunkCount int64        `json:"chunk_count"`
	Missing    []ChunkRange `json:"missing"`
}

func (m MissingChunks) Validate() error {
	if err := m.Page.validateCount(len(m.Missing)); err != nil {
		return err
	}
	if err := m.UploadID.Validate(); err != nil {
		return err
	}
	if m.ChunkCount < 0 || m.Missing == nil || len(m.Missing) > MaxPageSize {
		return errors.New("invalid missing-chunk page")
	}
	var end int64 = -1
	for _, r := range m.Missing {
		if r.Start < 0 || r.Start >= r.End || r.End > m.ChunkCount || r.Start <= end {
			return errors.New("missing ranges must be ordered, disjoint and coalesced")
		}
		end = r.End
	}
	return nil
}

type ChunkAcknowledgement struct {
	UploadID protocol.ID `json:"upload_id"`
	protocol.Chunk
}

func (a ChunkAcknowledgement) Validate() error {
	if err := a.UploadID.Validate(); err != nil {
		return err
	}
	return a.Chunk.Validate()
}

// ValidateFinalize binds an envelope to the authenticated account and the
// immutable upload intent. Durable objects and catalog CAS are checked later.
func ValidateFinalize(m protocol.Manifest, u Upload, scope protocol.Scope, limits protocol.TransferLimits, privateFormats []string) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := u.Validate(limits); err != nil {
		return err
	}
	if err := m.Validate(limits, privateFormats); err != nil {
		return err
	}
	if m.Scope != scope || m.FileID != u.FileID || m.VersionID != u.VersionID || m.Mode != u.Mode || int64(len(m.Chunks)) != u.ChunkCount || m.TotalBytes != u.TransmittedBytes {
		return errors.New("manifest does not match scoped upload intent")
	}
	if u.State != protocol.UploadOpen && u.State != protocol.UploadFinalizing {
		return errors.New("upload cannot be finalized")
	}
	return nil
}

type FinalizeResult struct {
	UploadID        protocol.ID          `json:"upload_id"`
	FileID          protocol.ID          `json:"file_id"`
	VersionID       protocol.ID          `json:"version_id"`
	State           protocol.UploadState `json:"state"`
	CatalogRevision protocol.Revision    `json:"catalog_revision"`
	HeadRevision    protocol.Revision    `json:"head_revision"`
}

func (r FinalizeResult) Validate() error {
	for _, id := range []protocol.ID{r.UploadID, r.FileID, r.VersionID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if r.State != protocol.UploadCommitted && r.State != protocol.UploadConflicted {
		return errors.New("finalization has no durable result")
	}
	for _, revision := range []protocol.Revision{r.CatalogRevision, r.HeadRevision} {
		if err := revision.Validate(); err != nil {
			return err
		}
		if revision == "0" {
			return errors.New("finalization requires positive revisions")
		}
	}
	return nil
}

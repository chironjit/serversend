// Package uploads implements the standard-mode upload lifecycle and immutable
// downloads for the vertical slice: create, chunk, reconcile, finalize, list
// and read. Every operation is scoped to the authenticated account.
package uploads

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// Lifetime bounds how long an unfinished upload keeps accepting chunks.
// Expired uploads are retired at startup, during maintenance and before creation.
const Lifetime = 24 * time.Hour

// Errors are already safe, typed API errors; callers return them unchanged.
var (
	errNotFound = api.Error{Code: api.NotFound, Message: "No such upload or file for this account"}
	errGone     = api.Error{Code: api.UploadGone, Message: "This upload has expired or was already finished; start a new upload"}
)

// StoragePolicy uses byte counts; zero quota means unlimited. MinFree is always
// preserved when accepting reservations and writing chunks.
type StoragePolicy struct{ GlobalBytes, UserBytes, MinFreeBytes int64 }

type Service struct {
	mu       sync.Mutex
	policy   StoragePolicy
	repo     *database.Repository
	store    *storage.Store
	serverID protocol.ID
	limits   protocol.TransferLimits
	now      func() time.Time
}

func New(repo *database.Repository, store *storage.Store, serverID protocol.ID, limits protocol.TransferLimits, now func() time.Time, policy ...StoragePolicy) (*Service, error) {
	if repo == nil || store == nil || serverID.Validate() != nil || limits.Validate() != nil {
		return nil, errors.New("uploads require a repository, object store, server ID and valid limits")
	}
	if now == nil {
		now = time.Now
	}
	s := &Service{repo: repo, store: store, serverID: serverID, limits: limits, now: now}
	s.policy.MinFreeBytes = 64 << 20
	if len(policy) > 0 {
		s.policy = policy[0]
	}
	if s.policy.GlobalBytes < 0 || s.policy.UserBytes < 0 || s.policy.MinFreeBytes < 0 {
		return nil, errors.New("negative storage policy")
	}
	if err := s.Maintain(context.Background()); err != nil {
		return nil, err
	}
	if err := s.store.Reconcile(context.Background(), s.repo.ObjectReferenced); err != nil {
		return nil, err
	}
	if err := s.repo.CheckObjects(context.Background(), s.store.VerifyExists); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Limits() protocol.TransferLimits { return s.limits }

func newID() protocol.ID {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return protocol.ID(hex.EncodeToString(value))
}

func revision(n uint64) protocol.Revision { return protocol.Revision(strconv.FormatUint(n, 10)) }

func toAPI(t database.Transfer) api.Upload {
	return api.Upload{ID: t.ID, State: t.State, ExpiresAt: t.ExpiresAt, CreateUploadRequest: api.CreateUploadRequest{
		ParentID: t.ParentID, FileID: t.FileID, VersionID: t.VersionID, Mode: t.Mode, ExpectedHead: revision(t.ExpectedHead),
		ChunkCount: t.ChunkCount, TransmittedBytes: t.TransmittedBytes, ManifestBytes: t.ManifestBytes,
	}}
}

// Create records the upload intent. Replaying the same key with the same
// intent returns the same upload (created false); a different intent conflicts.
func (s *Service) Create(ctx context.Context, account protocol.ID, key string, request api.CreateUploadRequest) (api.Upload, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" || len(key) > 128 {
		return api.Upload{}, false, api.Error{Code: api.InvalidRequest, Message: "Send an Idempotency-Key header of 1 to 128 characters"}
	}
	if err := request.Validate(s.limits); err != nil {
		return api.Upload{}, false, api.Error{Code: api.InvalidRequest, Message: "Upload request is outside the server's advertised limits"}
	}
	if request.Mode != protocol.Standard {
		return api.Upload{}, false, api.Error{Code: api.UnsupportedFormat, Message: "This server does not accept private uploads yet"}
	}
	if err := s.repo.CheckDirectory(ctx, account, request.ParentID); err != nil {
		return api.Upload{}, false, err
	}
	head, err := strconv.ParseUint(string(request.ExpectedHead), 10, 63)
	if err != nil {
		return api.Upload{}, false, api.Error{Code: api.InvalidRequest, Message: "Expected head revision is out of range"}
	}
	if err := s.maintain(ctx); err != nil {
		return api.Upload{}, false, err
	}
	available, err := s.store.Available()
	if err != nil {
		return api.Upload{}, false, err
	}
	available -= s.policy.MinFreeBytes
	if available < 0 {
		available = 0
	}
	now := s.now().UTC()
	recorded, created, err := s.repo.CreateTransfer(ctx, database.Transfer{
		ParentID: request.ParentID, AccountID: account, ID: newID(), FileID: request.FileID, VersionID: request.VersionID, Mode: request.Mode, IdempotencyKey: key,
		ExpectedHead: head, ChunkCount: request.ChunkCount, TransmittedBytes: request.TransmittedBytes, ManifestBytes: request.ManifestBytes,
		CreatedAt: now, ExpiresAt: now.Add(Lifetime),
	}, database.Capacity{Global: s.policy.GlobalBytes, Account: s.policy.UserBytes, Available: available})
	if err != nil {
		return api.Upload{}, false, err
	}
	upload := toAPI(recorded)
	if !created && upload.CreateUploadRequest != request {
		return api.Upload{}, false, api.Error{Code: api.Conflict, Message: "This idempotency key was already used for a different upload"}
	}
	return upload, created, nil
}

func (s *Service) transfer(ctx context.Context, account, upload protocol.ID) (database.Transfer, error) {
	if upload.Validate() != nil {
		return database.Transfer{}, errNotFound
	}
	t, err := s.repo.Transfer(ctx, account, upload)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound
	}
	return t, err
}

func (s *Service) Get(ctx context.Context, account, upload protocol.ID) (api.Upload, error) {
	t, err := s.transfer(ctx, account, upload)
	return toAPI(t), err
}

// Missing reports the chunk indexes not yet durably acknowledged, as ordered
// coalesced ranges. The slice returns the whole snapshot in one page.
func (s *Service) Missing(ctx context.Context, account, upload protocol.ID) (api.MissingChunks, error) {
	t, err := s.transfer(ctx, account, upload)
	if err != nil {
		return api.MissingChunks{}, err
	}
	if t.State == protocol.UploadCancelled || t.State == protocol.UploadExpired || (t.State == protocol.UploadOpen && !t.ExpiresAt.After(s.now())) {
		return api.MissingChunks{}, errGone
	}
	stored, err := s.repo.StoredChunks(ctx, account, upload)
	if err != nil {
		return api.MissingChunks{}, err
	}
	result := api.MissingChunks{Page: api.Page{Snapshot: newID()}, UploadID: upload, ChunkCount: t.ChunkCount, Missing: []api.ChunkRange{}}
	next := int64(0)
	for _, chunk := range append(stored, database.StoredChunk{Index: t.ChunkCount}) {
		if chunk.Index > next && len(result.Missing) < api.MaxPageSize {
			result.Missing = append(result.Missing, api.ChunkRange{Start: next, End: chunk.Index})
		}
		next = chunk.Index + 1
	}
	return result, nil
}

// PutChunk acknowledges a chunk only after its bytes are verified, flushed
// and published, and its metadata is committed. An identical retry returns
// the same acknowledgement (created false); different bytes conflict.
func (s *Service) PutChunk(ctx context.Context, account, upload protocol.ID, index, length int64, digest protocol.Digest, body io.Reader) (api.ChunkAcknowledgement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.transfer(ctx, account, upload)
	if err != nil {
		return api.ChunkAcknowledgement{}, false, err
	}
	if t.State == protocol.UploadCancelled || t.State == protocol.UploadExpired || (t.State == protocol.UploadOpen && !t.ExpiresAt.After(s.now())) {
		return api.ChunkAcknowledgement{}, false, errGone
	}
	if index < 0 || index >= t.ChunkCount || digest.Validate() != nil || s.limits.ValidateChunkSize(length, index == t.ChunkCount-1) != nil {
		return api.ChunkAcknowledgement{}, false, api.Error{Code: api.InvalidRequest, Message: "Chunk index, length or digest is outside this upload's bounds"}
	}
	chunk := database.StoredChunk{Index: index, Bytes: length, Digest: digest, ObjectID: newID()}
	existing, err := s.repo.StoredChunk(ctx, account, upload, index)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		if t.State != protocol.UploadOpen {
			return api.ChunkAcknowledgement{}, false, errGone
		}
		remaining, remainingErr := s.repo.UnwrittenBytes(ctx, account, upload)
		if remainingErr != nil {
			return api.ChunkAcknowledgement{}, false, remainingErr
		}
		if length > remaining {
			return api.ChunkAcknowledgement{}, false, api.Error{Code: api.InvalidRequest, Message: "Chunk exceeds the remaining upload reservation"}
		}
		available, spaceErr := s.store.Available()
		if spaceErr != nil {
			return api.ChunkAcknowledgement{}, false, spaceErr
		}
		if available < s.policy.MinFreeBytes || length > available-s.policy.MinFreeBytes {
			return api.ChunkAcknowledgement{}, false, api.Error{Code: api.CapacityExhausted, Message: "Insufficient free space to store this chunk"}
		}
		if err := s.store.Put(chunk.ObjectID, body, length, digest); err != nil {
			if errors.Is(err, storage.ErrMismatch) {
				return api.ChunkAcknowledgement{}, false, api.Error{Code: api.InvalidRequest, Message: "Chunk bytes do not match the declared length and SHA-256; send the chunk again"}
			}
			return api.ChunkAcknowledgement{}, false, err
		}
		existing, created, err = s.repo.RecordChunk(ctx, account, upload, chunk, s.now())
		if err == nil && !created {
			s.store.Remove(chunk.ObjectID)
		}
		if errors.Is(err, database.ErrUploadClosed) {
			err = errGone
		}
	}
	if err != nil {
		return api.ChunkAcknowledgement{}, false, err
	}
	if existing.Bytes != length || subtle.ConstantTimeCompare([]byte(existing.Digest), []byte(digest)) != 1 {
		return api.ChunkAcknowledgement{}, false, api.Error{Code: api.Conflict, Message: "A different chunk was already stored at this index"}
	}
	return api.ChunkAcknowledgement{UploadID: upload, Chunk: protocol.Chunk{Index: index, Bytes: length, SHA256: digest}}, created, nil
}

// Finalize publishes the version only when the exact manifest matches the
// upload intent and every listed chunk is the stored object. Replaying the
// same manifest returns the recorded result.
func (s *Service) Finalize(ctx context.Context, account, upload protocol.ID, body []byte) (api.FinalizeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.transfer(ctx, account, upload)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if t.State == protocol.UploadCancelled || t.State == protocol.UploadExpired || (t.State == protocol.UploadOpen && !t.ExpiresAt.After(s.now())) {
		return api.FinalizeResult{}, errGone
	}
	invalid := api.Error{Code: api.InvalidRequest, Message: "Manifest does not match this upload and its stored chunks"}
	intent := toAPI(t)
	if api.ValidateManifestBodyLength(int64(len(body)), intent) != nil {
		return api.FinalizeResult{}, invalid
	}
	manifest, err := api.DecodeManifest(bytes.NewReader(body), s.limits, nil)
	if err != nil {
		return api.FinalizeResult{}, invalid
	}
	finished := t.State == protocol.UploadCommitted || t.State == protocol.UploadConflicted
	if !finished {
		if api.ValidateFinalize(manifest, intent, protocol.Scope{ServerID: s.serverID, AccountID: account}, s.limits, nil) != nil {
			if t.State != protocol.UploadOpen {
				return api.FinalizeResult{}, errGone
			}
			return api.FinalizeResult{}, invalid
		}
		stored, err := s.repo.StoredChunks(ctx, account, upload)
		if err != nil {
			return api.FinalizeResult{}, err
		}
		if len(stored) != len(manifest.Chunks) {
			return api.FinalizeResult{}, api.Error{Code: api.Conflict, Message: "Some chunks are still missing; query the missing chunks and upload them before finalizing"}
		}
		for i, chunk := range manifest.Chunks {
			if stored[i].Index != chunk.Index || stored[i].Bytes != chunk.Bytes || stored[i].Digest != chunk.SHA256 {
				return api.FinalizeResult{}, invalid
			}
		}
	}
	result, err := s.repo.CommitUpload(ctx, account, upload, manifest.Standard.Name, body, s.now().UTC())
	switch {
	case errors.Is(err, database.ErrUploadClosed):
		return api.FinalizeResult{}, errGone
	case errors.Is(err, database.ErrUploadConflict):
		return api.FinalizeResult{}, api.Error{Code: api.Conflict, Message: "This upload was already finalized with a different manifest, or its version already exists"}
	case err != nil:
		return api.FinalizeResult{}, err
	}
	return api.FinalizeResult{UploadID: upload, FileID: result.FileID, VersionID: result.VersionID, State: result.State, CatalogRevision: revision(result.CatalogRevision), HeadRevision: revision(result.HeadRevision)}, nil
}

func (s *Service) Catalog(ctx context.Context, account, parent protocol.ID) (api.CatalogPage, error) {
	current, files, err := s.repo.Catalog(ctx, account, parent, api.MaxPageSize)
	if err != nil {
		return api.CatalogPage{}, err
	}
	page := api.CatalogPage{Page: api.Page{Snapshot: newID()}, CatalogRevision: revision(current), Entries: []api.CatalogEntry{}}
	for _, file := range files {
		kind := api.File
		if file.Directory {
			kind = api.Directory
		}
		page.Entries = append(page.Entries, api.CatalogEntry{ID: file.ID, ParentID: file.ParentID, Kind: kind, Mode: protocol.Standard, HeadVersionID: file.HeadVersionID,
			HeadRevision: revision(file.HeadRevision), Standard: &protocol.StandardMetadata{Name: file.Name}})
	}
	return page, nil
}

// Manifest returns the exact bytes accepted at finalization.
func (s *Service) Manifest(ctx context.Context, account, file, version protocol.ID) ([]byte, error) {
	manifest, _, err := s.version(ctx, account, file, version)
	return manifest, err
}

func (s *Service) version(ctx context.Context, account, file, version protocol.ID) ([]byte, protocol.ID, error) {
	if file.Validate() != nil || version.Validate() != nil {
		return nil, "", errNotFound
	}
	manifest, upload, err := s.repo.Version(ctx, account, file, version)
	if errors.Is(err, sql.ErrNoRows) {
		err = errNotFound
	}
	return manifest, upload, err
}

// OpenChunk returns one immutable stored chunk; the caller closes the file.
func (s *Service) OpenChunk(ctx context.Context, account, file, version protocol.ID, index int64) (*os.File, protocol.Chunk, error) {
	_, upload, err := s.version(ctx, account, file, version)
	if err != nil {
		return nil, protocol.Chunk{}, err
	}
	stored, err := s.repo.StoredChunk(ctx, account, upload, index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, protocol.Chunk{}, errNotFound
	}
	if err != nil {
		return nil, protocol.Chunk{}, err
	}
	object, err := s.store.Open(stored.ObjectID)
	if err != nil {
		return nil, protocol.Chunk{}, api.Error{Code: api.SourceMissing, Message: "Stored data for this file is missing on the server; contact the server operator"}
	}
	return object, protocol.Chunk{Index: index, Bytes: stored.Bytes, SHA256: stored.Digest}, nil
}

// Available reports usable free bytes on the object filesystem.
func (s *Service) Available() (int64, error) { return s.store.Available() }

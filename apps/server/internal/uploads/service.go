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
	"fmt"
	"github.com/chironjit/serversend/shared/vault"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/layout"
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
	readers int // open version streams; protected by mu

	statsGate       chan struct{}
	statsCache      map[protocol.ID]api.StorageStatistics
	stageMu         sync.Mutex
	stages          map[string]protocol.ID
	traffic         trafficCounters
	capacityPaths   map[string]string
	catalogKey      [32]byte
	catalogInstance protocol.ID
	mu              sync.Mutex
	writersMu       sync.Mutex
	writers         map[string]bool // chunk writers in progress
	policy          StoragePolicy
	repo            *database.Repository
	store           *storage.Store
	serverID        protocol.ID
	limits          protocol.TransferLimits
	now             func() time.Time
}

func New(repo *database.Repository, store *storage.Store, serverID protocol.ID, limits protocol.TransferLimits, now func() time.Time, policy ...StoragePolicy) (*Service, error) {
	if repo == nil || store == nil || serverID.Validate() != nil || limits.Validate() != nil {
		return nil, errors.New("uploads require a repository, object store, server ID and valid limits")
	}
	if now == nil {
		now = time.Now
	}
	s := &Service{statsGate: make(chan struct{}, 1), statsCache: map[protocol.ID]api.StorageStatistics{}, stages: map[string]protocol.ID{}, repo: repo, store: store, serverID: serverID, limits: limits, now: now}
	if _, err := rand.Read(s.catalogKey[:]); err != nil {
		return nil, err
	}
	s.catalogInstance = newID()
	s.policy.MinFreeBytes = 64 << 20
	if len(policy) > 0 {
		s.policy = policy[0]
	}
	if s.policy.GlobalBytes < 0 || s.policy.UserBytes < 0 || s.policy.MinFreeBytes < 0 {
		return nil, errors.New("negative storage policy")
	}
	if err := s.recover(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// recover runs before requests are accepted, under the deployment lock: it
// finishes interrupted filesystem changes, discards unpublished and orphaned
// upload files, then refuses missing or truncated acknowledged contents.
func (s *Service) recover(ctx context.Context) error {
	if err := s.applyJournal(ctx); err != nil {
		return err
	}
	// Forget replayed changes before cleanup makes them unsafe to repeat.
	if err := s.repo.ClearJournal(ctx); err != nil {
		return err
	}
	if err := s.Maintain(ctx); err != nil {
		return err
	}
	if err := errors.Join(s.store.ClearTmp(), s.store.ClearTrash()); err != nil {
		return err
	}
	err := s.store.CleanUploads(func(id protocol.ID) (bool, error) {
		open, end, err := s.repo.OpenUploadEnd(ctx, id)
		if err != nil || !open {
			return false, err
		}
		return true, s.store.Verify(layout.Upload(id), end, false)
	})
	if err != nil {
		return err
	}
	return s.repo.EachVersion(ctx, func(location database.VersionLocation) error {
		return s.store.Verify(location.Path, location.Bytes, true)
	})
}

// applyJournal performs and forgets every pending filesystem change. Each
// mutation calls it under s.mu after committing, so changes apply in order;
// if it fails, later mutations fail too until it succeeds.
func (s *Service) applyJournal(ctx context.Context) error {
	ops, last, err := s.repo.Journal(ctx)
	if err != nil || len(ops) == 0 {
		return err
	}
	if err := s.store.Apply(ops); err != nil {
		return fmt.Errorf("apply file storage changes: %w", err)
	}
	s.repo.Applied(last)
	return nil
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
	if request.Mode == protocol.Private {
		if _, err := s.repo.Vault(ctx, account); err != nil {
			return api.Upload{}, false, err
		}
	}
	if request.Mode != protocol.Standard && request.Mode != protocol.Private {
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
	id := newID()
	if err := s.store.CreateUpload(id); err != nil {
		return api.Upload{}, false, err
	}
	recorded, created, err := s.repo.CreateTransfer(ctx, database.Transfer{
		ParentID: request.ParentID, AccountID: account, ID: id, FileID: request.FileID, VersionID: request.VersionID, Mode: request.Mode, IdempotencyKey: key,
		ExpectedHead: head, ChunkCount: request.ChunkCount, TransmittedBytes: request.TransmittedBytes, ManifestBytes: request.ManifestBytes,
		CreatedAt: now, ExpiresAt: now.Add(Lifetime),
	}, database.Capacity{Global: s.policy.GlobalBytes, Account: s.policy.UserBytes, Available: available})
	if err != nil || !created {
		s.store.RemoveUpload(id) // a replay keeps its original file
	}
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

// PutChunk acknowledges a chunk only after its bytes are verified and
// flushed at their offset in the upload's file, and its metadata is
// committed. An identical retry returns the same acknowledgement (created
// false); different bytes conflict.
//
// Every non-final chunk has one size, fixed by the first to arrive, so each
// chunk has exactly one offset and chunks never overlap; the final chunk
// ends the file. One writer per chunk runs at a time. Receiving, hashing and
// flushing run outside the mutation lock so concurrent uploads overlap.
// Concurrent chunks may pass the free-space pre-check together; upload
// reservations already cover their bytes.
func (s *Service) PutChunk(ctx context.Context, account, upload protocol.ID, index, length int64, digest protocol.Digest, body io.Reader) (api.ChunkAcknowledgement, bool, error) {
	counter := &countedReader{Reader: body}
	body = counter
	var useful int64
	defer func() { s.RecordTraffic(account, counter.n, useful, max(0, counter.n-useful)) }()
	t, err := s.transfer(ctx, account, upload)
	if err != nil {
		return api.ChunkAcknowledgement{}, false, err
	}
	if t.State == protocol.UploadCancelled || t.State == protocol.UploadExpired || (t.State == protocol.UploadOpen && !t.ExpiresAt.After(s.now())) {
		return api.ChunkAcknowledgement{}, false, errGone
	}
	final := index == t.ChunkCount-1
	if index < 0 || index >= t.ChunkCount || digest.Validate() != nil || s.limits.ValidateChunkSize(length, final) != nil {
		return api.ChunkAcknowledgement{}, false, api.Error{Code: api.InvalidRequest, Message: "Chunk index, length or digest is outside this upload's bounds"}
	}
	existing, err := s.repo.StoredChunk(ctx, account, upload, index)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		if t.State != protocol.UploadOpen {
			return api.ChunkAcknowledgement{}, false, errGone
		}
		offset, offsetErr := s.chunkOffset(ctx, t, index, length)
		if offsetErr != nil {
			return api.ChunkAcknowledgement{}, false, offsetErr
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
		release, busy := s.claimWriter(upload, index)
		if busy {
			return api.ChunkAcknowledgement{}, false, api.Error{Code: api.Conflict, Message: "This chunk is already being received; retry shortly"}
		}
		defer release()
		if writeErr := s.store.WriteChunk(upload, offset, length, digest, body); writeErr != nil {
			switch {
			case errors.Is(writeErr, storage.ErrMismatch):
				return api.ChunkAcknowledgement{}, false, api.Error{Code: api.InvalidRequest, Message: "Chunk bytes do not match the declared length and SHA-256; send the chunk again"}
			case errors.Is(writeErr, storage.ErrGone):
				return api.ChunkAcknowledgement{}, false, errGone
			}
			return api.ChunkAcknowledgement{}, false, writeErr
		}
		chunk := database.StoredChunk{Index: index, Bytes: length, Digest: digest, Offset: offset}
		s.mu.Lock()
		existing, created, err = s.repo.RecordChunk(ctx, account, upload, chunk, s.now())
		s.mu.Unlock()
		if errors.Is(err, database.ErrUploadClosed) {
			err = errGone
		}
	}
	if err != nil {
		return api.ChunkAcknowledgement{}, false, err
	}
	if !created && err == nil {
		if _, readErr := io.Copy(io.Discard, io.LimitReader(body, length)); readErr != nil {
			return api.ChunkAcknowledgement{}, false, readErr
		}
	}
	if existing.Bytes != length || subtle.ConstantTimeCompare([]byte(existing.Digest), []byte(digest)) != 1 {
		return api.ChunkAcknowledgement{}, false, api.Error{Code: api.Conflict, Message: "A different chunk was already stored at this index"}
	}
	if created {
		useful = length
	}
	return api.ChunkAcknowledgement{UploadID: upload, Chunk: protocol.Chunk{Index: index, Bytes: length, SHA256: digest}}, created, nil
}

// chunkOffset places a chunk: non-final chunks at index times the upload's
// chunk size, and the final chunk at the end of the file.
func (s *Service) chunkOffset(ctx context.Context, t database.Transfer, index, length int64) (int64, error) {
	invalid := api.Error{Code: api.InvalidRequest, Message: "Every chunk except the last must have the same size, and the last must end the file"}
	size := t.ChunkBytes
	if index < t.ChunkCount-1 && size == 0 {
		if length*(t.ChunkCount-1) >= t.TransmittedBytes || t.TransmittedBytes-length*(t.ChunkCount-1) > length {
			return 0, invalid
		}
		claimed, err := s.repo.ClaimChunkSize(ctx, t.AccountID, t.ID, length)
		if err != nil {
			return 0, err
		}
		size = claimed
	}
	if index == t.ChunkCount-1 {
		offset := t.TransmittedBytes - length
		if offset < 0 || size != 0 && offset != size*index {
			return 0, invalid
		}
		return offset, nil
	}
	if length != size {
		return 0, invalid
	}
	return index * size, nil
}

func (s *Service) claimWriter(upload protocol.ID, index int64) (func(), bool) {
	key := string(upload) + "/" + strconv.FormatInt(index, 10)
	s.writersMu.Lock()
	defer s.writersMu.Unlock()
	if s.writers[key] {
		return nil, true
	}
	if s.writers == nil {
		s.writers = map[string]bool{}
	}
	s.writers[key] = true
	return func() {
		s.writersMu.Lock()
		delete(s.writers, key)
		s.writersMu.Unlock()
	}, false
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
	manifest, err := api.DecodeManifest(bytes.NewReader(body), s.limits, vault.Formats)
	if err != nil {
		return api.FinalizeResult{}, invalid
	}
	finished := t.State == protocol.UploadCommitted || t.State == protocol.UploadConflicted
	if !finished {
		if api.ValidateFinalize(manifest, intent, protocol.Scope{ServerID: s.serverID, AccountID: account}, s.limits, vault.Formats) != nil {
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
	if err := s.applyJournal(ctx); err != nil {
		return api.FinalizeResult{}, err
	}
	if !finished {
		if err := s.store.FinishUpload(upload, t.TransmittedBytes); errors.Is(err, storage.ErrGone) {
			return api.FinalizeResult{}, errGone
		} else if err != nil {
			return api.FinalizeResult{}, err
		}
	}
	name := string(manifest.FileID)
	if manifest.Standard != nil {
		name = manifest.Standard.Name
	}
	result, err := s.repo.CommitUpload(ctx, account, upload, name, body, s.now().UTC())
	if err == nil {
		err = s.applyJournal(ctx)
	}
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

type chunkReader struct {
	*io.SectionReader
	io.Closer
}

// OpenChunk returns one immutable stored chunk; the caller closes it. The
// file is opened under the mutation lock, so a concurrent replacement moves
// it only after it is open and the reader keeps the version it located.
func (s *Service) OpenChunk(ctx context.Context, account, file, version protocol.ID, index int64) (io.ReadCloser, protocol.Chunk, error) {
	if file.Validate() != nil || version.Validate() != nil {
		return nil, protocol.Chunk{}, errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyJournal(ctx); err != nil {
		return nil, protocol.Chunk{}, err
	}
	location, err := s.repo.VersionLocation(ctx, account, file, version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, protocol.Chunk{}, errNotFound
	}
	if err != nil {
		return nil, protocol.Chunk{}, err
	}
	stored, err := s.repo.StoredChunk(ctx, account, location.Upload, index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, protocol.Chunk{}, errNotFound
	}
	if err != nil {
		return nil, protocol.Chunk{}, err
	}
	f, err := s.store.Open(location.Path)
	if err != nil {
		return nil, protocol.Chunk{}, api.Error{Code: api.SourceMissing, Message: "Stored data for this file is missing on the server; contact the server operator"}
	}
	s.readers++
	return chunkReader{io.NewSectionReader(f, stored.Offset, stored.Bytes), &versionReaderCloser{close: func() error { err := f.Close(); s.mu.Lock(); s.readers--; s.mu.Unlock(); return err }}}, protocol.Chunk{Index: index, Bytes: stored.Bytes, SHA256: stored.Digest}, nil
}

// Available reports usable free bytes on the object filesystem.
func (s *Service) Available() (int64, error) { return s.store.Available() }

// Keep collection away from open streams on every supported filesystem.
type versionReaderCloser struct {
	once  sync.Once
	close func() error
	err   error
}

func (c *versionReaderCloser) Close() error { c.once.Do(func() { c.err = c.close() }); return c.err }

package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// UploadBatch publishes small files with one concurrent flush round and one
// metadata transaction, instead of three transactions and a flush per file.
// Each file is validated exactly as Create and Finalize would; refusals are
// reported per file in request order. body holds the files' bytes in order.
func (s *Service) UploadBatch(ctx context.Context, account protocol.ID, request api.BatchUploadRequest, body io.Reader) ([]api.BatchUploadResult, error) {
	var total int64
	for _, file := range request.Files {
		total += file.TransmittedBytes
	}
	available, err := s.store.Available()
	if err != nil {
		return nil, err
	}
	available -= s.policy.MinFreeBytes
	if available < 0 || total > available {
		return nil, api.Error{Code: api.CapacityExhausted, Message: "Insufficient free space to store these files"}
	}

	results := make([]api.BatchUploadResult, len(request.Files))
	refuse := func(i int, err api.Error) { results[i].Error = &err }
	invalid := api.Error{Code: api.InvalidRequest, Message: "Manifest does not match this upload"}
	files := make([]database.BatchFile, 0, len(request.Files))
	positions := make([]int, 0, len(request.Files))
	ids := []protocol.ID{}
	staged := []*storage.Staged{}
	defer func() {
		for _, st := range staged {
			st.Discard()
		}
	}()
	// One shard per batch means one shard-directory flush.
	prefix := string(newID()[:2])
	scope := protocol.Scope{ServerID: s.serverID, AccountID: account}
	now := s.now().UTC()
	for i, file := range request.Files {
		manifest, err := api.DecodeManifest(bytes.NewReader(file.Manifest), s.limits, nil)
		head, headErr := strconv.ParseUint(string(file.ExpectedHead), 10, 63)
		intent := api.Upload{ID: newID(), CreateUploadRequest: file.CreateUploadRequest, State: protocol.UploadOpen, ExpiresAt: now.Add(Lifetime)}
		if err != nil || headErr != nil || file.Mode != protocol.Standard || manifest.Standard == nil || api.ValidateFinalize(manifest, intent, scope, s.limits, nil) != nil {
			refuse(i, invalid)
			if _, err := io.CopyN(io.Discard, body, file.TransmittedBytes); err != nil {
				return nil, err
			}
			continue
		}
		entry := database.BatchFile{Name: manifest.Standard.Name, Manifest: file.Manifest, Transfer: database.Transfer{
			ID: intent.ID, ParentID: file.ParentID, FileID: file.FileID, VersionID: file.VersionID, Mode: file.Mode, IdempotencyKey: file.IdempotencyKey,
			ExpectedHead: head, ChunkCount: file.ChunkCount, TransmittedBytes: file.TransmittedBytes, ManifestBytes: file.ManifestBytes,
		}}
		if file.ChunkCount == 1 {
			chunk := manifest.Chunks[0]
			// Stage consumes exactly this file's bytes, even on a mismatch.
			st, err := s.store.Stage(io.LimitReader(body, file.TransmittedBytes), file.TransmittedBytes, chunk.SHA256)
			if errors.Is(err, storage.ErrMismatch) {
				refuse(i, api.Error{Code: api.InvalidRequest, Message: "File bytes do not match the declared length and SHA-256; send the file again"})
				continue
			}
			if err != nil {
				return nil, err
			}
			id := protocol.ID(prefix + string(newID()[2:]))
			entry.Chunk = &database.StoredChunk{Bytes: chunk.Bytes, Digest: chunk.SHA256, ObjectID: id}
			ids, staged = append(ids, id), append(staged, st)
		}
		files, positions = append(files, entry), append(positions, i)
	}
	if n, _ := body.Read(make([]byte, 1)); n != 0 {
		return nil, api.Error{Code: api.InvalidRequest, Message: "Batch body is longer than its declared files"}
	}
	publish := staged
	staged = nil
	if err := s.store.Publish(ids, publish); err != nil {
		return nil, err
	}

	// Unrecorded objects are invisible to cleanup, as in PutChunk.
	s.mu.Lock()
	outcomes, err := s.repo.CommitBatch(ctx, account, files, database.Capacity{Global: s.policy.GlobalBytes, Account: s.policy.UserBytes, Available: available}, now)
	s.mu.Unlock()
	if err != nil {
		var refused api.Error
		if errors.As(err, &refused) { // definitely rolled back
			for _, id := range ids {
				s.store.Remove(id)
			}
		}
		// Otherwise startup reconciliation removes any unreferenced objects.
		return nil, err
	}
	for k, outcome := range outcomes {
		i := positions[k]
		if !outcome.Inserted && files[k].Chunk != nil {
			s.store.Remove(files[k].Chunk.ObjectID)
		}
		if outcome.Err != nil {
			var refused api.Error
			errors.As(outcome.Err, &refused)
			refuse(i, refused)
			continue
		}
		t := outcome.Result
		results[i].Result = &api.FinalizeResult{UploadID: t.ID, FileID: t.FileID, VersionID: t.VersionID, State: t.State, CatalogRevision: revision(t.CatalogRevision), HeadRevision: revision(t.HeadRevision)}
	}
	return results, nil
}

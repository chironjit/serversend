package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

// UploadCheckpoint is public local queue metadata, scoped by its manifest.
// Persist it atomically in save; it contains no credential. A checkpoint must
// belong to only one active worker. Acknowledgements are compact half-open
// ranges and are hints: the server's missing query controls resumption.
type UploadCheckpoint struct {
	Manifest          protocol.Manifest       `json:"manifest"`
	Intent            api.CreateUploadRequest `json:"intent"`
	UploadID          protocol.ID             `json:"upload_id,omitempty"`
	IdempotencyKey    string                  `json:"idempotency_key"`
	SourceFingerprint protocol.Digest         `json:"source_fingerprint"`
	SourceSize        int64                   `json:"source_size"`
	SourceModTimeNS   int64                   `json:"source_mod_time_ns"`
	Acknowledged      []api.ChunkRange        `json:"acknowledged,omitempty"`
	AcknowledgedBytes int64                   `json:"acknowledged_bytes"`
	Result            *api.FinalizeResult     `json:"result,omitempty"`
}

// CancelUpload releases an open upload's reservation. A concurrent finalization
// can win; Conflict is returned so the queue can reconcile its actual outcome.
func (c *Client) CancelUpload(ctx context.Context, id protocol.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	return retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "DELETE", path: "/uploads/" + string(id), token: c.token()}, nil, nil)
	})
}

// UploadResumable persists immutable identities before admission and reconciles
// missing chunks on every resumed call. Cancellation interrupts local work but
// deliberately retains the upload; queues use CancelUpload for explicit cancel.
// Source replacement creates a fresh generation only after retiring the old one.
func (c *Client) UploadResumable(ctx context.Context, path string, parent protocol.ID, checkpoint *UploadCheckpoint, save func(*UploadCheckpoint) error, progress Progress) (api.FinalizeResult, error) {
	if checkpoint == nil || save == nil {
		return api.FinalizeResult{}, errors.New("upload checkpoint and persistence callback are required")
	}
	account, signedIn := c.Account()
	if !signedIn {
		return api.FinalizeResult{}, api.Error{Code: api.AuthenticationRequired, Message: "Sign in before uploading"}
	}
	scope := protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}
	var file *os.File
	var info os.FileInfo
	// Inspect a fresh source before even fetching capabilities; changes during
	// that request must not silently become a different selected generation.
	if checkpoint.Manifest.FileID == "" {
		var e error
		file, info, e = openUploadSource(path)
		if e != nil {
			return api.FinalizeResult{}, e
		}
		defer file.Close()
		if e = save(checkpoint); e != nil {
			return api.FinalizeResult{}, e
		}
	}
	var limits protocol.TransferLimits
	err := retryTransfer(ctx, func() error { var e error; limits, e = c.transferLimits(ctx); return e })
	if err != nil {
		return api.FinalizeResult{}, err
	}
	resumed := checkpoint.Manifest.FileID != ""
	if resumed {
		if checkpoint.Manifest.Scope != scope || checkpoint.Intent.ParentID != parent {
			return api.FinalizeResult{}, errors.New("upload checkpoint belongs to another server, account or destination")
		}
		if err := checkpoint.Manifest.Validate(limits, nil); err != nil {
			return api.FinalizeResult{}, fmt.Errorf("invalid upload checkpoint: %w", err)
		}
		if err := checkpoint.validate(limits); err != nil {
			return api.FinalizeResult{}, err
		}
		if checkpoint.Result != nil {
			return checkpointResult(checkpoint)
		}
		if checkpoint.UploadID == "" {
			if err = c.admitCheckpoint(ctx, checkpoint, save); err != nil {
				return api.FinalizeResult{}, err
			}
		}
		if checkpoint.UploadID != "" {
			var upload api.Upload
			err = retryTransfer(ctx, func() error {
				return c.exchange(ctx, call{method: "GET", path: "/uploads/" + string(checkpoint.UploadID), token: c.token()}, nil, &upload)
			})
			var failure api.Error
			if errors.As(err, &failure) && (failure.Code == api.UploadGone || failure.Code == api.NotFound) {
				*checkpoint = UploadCheckpoint{}
				if err = save(checkpoint); err != nil {
					return api.FinalizeResult{}, err
				}
				resumed = false
			} else if err != nil {
				return api.FinalizeResult{}, err
			} else {
				if upload.ID != checkpoint.UploadID || upload.CreateUploadRequest != checkpoint.Intent {
					return api.FinalizeResult{}, errors.New("server upload differs from the persisted intent")
				}
				switch upload.State {
				case protocol.UploadCommitted, protocol.UploadConflicted:
					return c.finalizeCheckpoint(ctx, checkpoint, save)
				case protocol.UploadCancelled, protocol.UploadExpired:
					*checkpoint = UploadCheckpoint{}
					if err = save(checkpoint); err != nil {
						return api.FinalizeResult{}, err
					}
					resumed = false
				}
			}
		}
	}
	if file == nil {
		file, info, err = openUploadSource(path)
		if err != nil {
			return api.FinalizeResult{}, err
		}
		defer file.Close()
	}
	prepared, err := prepareUpload(ctx, file, info, filepath.Base(path), scope, parent, limits, checkpoint)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if resumed && prepared.SourceFingerprint != checkpoint.SourceFingerprint {
		if checkpoint.UploadID != "" {
			if err = c.CancelUpload(ctx, checkpoint.UploadID); err != nil {
				return api.FinalizeResult{}, err
			}
		}
		*checkpoint = prepared
		resumed = false
	} else if !resumed {
		*checkpoint = prepared
	}
	// Also saves an unchanged checkpoint before a replayed admission, ensuring
	// a failed database write can never be followed by network mutations.
	if err = save(checkpoint); err != nil {
		return api.FinalizeResult{}, err
	}
	if checkpoint.UploadID == "" {
		if err = c.admitCheckpoint(ctx, checkpoint, save); err != nil {
			return api.FinalizeResult{}, err
		}
	}

	bufferSize := int64(1)
	for _, chunk := range checkpoint.Manifest.Chunks {
		bufferSize = max(bufferSize, chunk.Bytes)
	}
	buffer := make([]byte, bufferSize)
	if progress != nil {
		progress(checkpoint.AcknowledgedBytes, checkpoint.Manifest.TotalBytes)
	}
	if !resumed {
		for _, chunk := range checkpoint.Manifest.Chunks {
			if err = c.sendCheckpointChunk(ctx, file, buffer, checkpoint, chunk.Index, save, progress); err != nil {
				return api.FinalizeResult{}, err
			}
		}
	} else {
		// The current server returns at most 1000 missing ranges. Requery after
		// sending them instead of interpreting a truncated page as complete. This
		// stays bounded and also recovers missing acknowledgements after crashes.
		for round := 0; ; round++ {
			if round > len(checkpoint.Manifest.Chunks) {
				return api.FinalizeResult{}, errors.New("server did not make progress acknowledging resumed chunks")
			}
			var missing api.MissingChunks
			err = retryTransfer(ctx, func() error {
				return c.exchange(ctx, call{method: "GET", path: "/uploads/" + string(checkpoint.UploadID) + "/missing", token: c.token()}, nil, &missing)
			})
			var gone api.Error
			if errors.As(err, &gone) && gone.Code == api.UploadGone {
				*checkpoint = UploadCheckpoint{}
				if err = save(checkpoint); err != nil {
					return api.FinalizeResult{}, err
				}
				return c.UploadResumable(ctx, path, parent, checkpoint, save, progress)
			}
			if err != nil {
				return api.FinalizeResult{}, err
			}
			if missing.Validate() != nil || missing.UploadID != checkpoint.UploadID || missing.ChunkCount != int64(len(checkpoint.Manifest.Chunks)) {
				return api.FinalizeResult{}, errors.New("server returned an invalid missing-chunk snapshot")
			}
			if len(missing.Missing) == 0 {
				break
			}
			for _, r := range missing.Missing {
				for index := r.Start; index < r.End; index++ {
					if err = c.sendCheckpointChunk(ctx, file, buffer, checkpoint, index, save, progress); err != nil {
						return api.FinalizeResult{}, err
					}
				}
			}
		}
	}
	// Do not publish a file that changed during the read/send pass, even if its
	// changed bytes happened to belong only to already-acknowledged chunks.
	current, err := file.Stat()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return api.FinalizeResult{}, ErrSourceChanged
	}
	return c.finalizeCheckpoint(ctx, checkpoint, save)
}

func prepareUpload(ctx context.Context, file *os.File, info os.FileInfo, name string, scope protocol.Scope, parent protocol.ID, limits protocol.TransferLimits, previous *UploadCheckpoint) (UploadCheckpoint, error) {
	if err := protocol.ValidateName(name); err != nil {
		return UploadCheckpoint{}, fmt.Errorf("%q cannot be used as a file name on the server", name)
	}
	chunkBytes := min(max(int64(preferredChunkBytes), limits.MinChunkBytes), limits.MaxChunkBytes)
	if info.Size()/chunkBytes >= limits.MaxChunks/2 || info.Size()/chunkBytes > limits.MaxManifestBytes/128 {
		chunkBytes = limits.MaxChunkBytes
	}
	// Preserve chunk boundaries across changes in advertised preferred limits.
	if previous.Manifest.FileID != "" && previous.SourceSize == info.Size() && len(previous.Manifest.Chunks) > 0 {
		chunkBytes = previous.Manifest.Chunks[0].Bytes
	}
	buffer := make([]byte, min(chunkBytes, max(info.Size(), 1)))
	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: scope, FileID: randomID(), VersionID: randomID(), Mode: protocol.Standard, Chunks: []protocol.Chunk{}, Standard: &protocol.StandardMetadata{Name: name}}
	for {
		if err := ctx.Err(); err != nil {
			return UploadCheckpoint{}, err
		}
		n, err := io.ReadFull(file, buffer)
		if n > 0 {
			if int64(n) > info.Size()-manifest.TotalBytes {
				return UploadCheckpoint{}, ErrSourceChanged
			}
			sum := sha256.Sum256(buffer[:n])
			manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(len(manifest.Chunks)), Bytes: int64(n), SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
			manifest.TotalBytes += int64(n)
			if int64(len(manifest.Chunks)) > limits.MaxChunks {
				return UploadCheckpoint{}, fmt.Errorf("%q is larger than this server version accepts", name)
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return UploadCheckpoint{}, err
		}
	}
	current, err := file.Stat()
	if err != nil {
		return UploadCheckpoint{}, err
	}
	if current.Size() != info.Size() || manifest.TotalBytes != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return UploadCheckpoint{}, ErrSourceChanged
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return UploadCheckpoint{}, err
	}
	if int64(len(body)) > limits.MaxManifestBytes {
		return UploadCheckpoint{}, fmt.Errorf("%q is larger than this server version accepts", name)
	}
	fingerprintBody, _ := json.Marshal(struct {
		Name          string
		Size, ModTime int64
		Chunks        []protocol.Chunk
	}{name, info.Size(), info.ModTime().UnixNano(), manifest.Chunks})
	fingerprint := sha256.Sum256(fingerprintBody)
	return UploadCheckpoint{Manifest: manifest, Intent: api.CreateUploadRequest{ParentID: parent, FileID: manifest.FileID, VersionID: manifest.VersionID, Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: int64(len(manifest.Chunks)), TransmittedBytes: manifest.TotalBytes, ManifestBytes: int64(len(body))}, IdempotencyKey: string(randomID()), SourceFingerprint: protocol.Digest(hex.EncodeToString(fingerprint[:])), SourceSize: info.Size(), SourceModTimeNS: info.ModTime().UnixNano()}, nil
}

func (c *Client) sendCheckpointChunk(ctx context.Context, file *os.File, buffer []byte, checkpoint *UploadCheckpoint, index int64, save func(*UploadCheckpoint) error, progress Progress) error {
	chunk := checkpoint.Manifest.Chunks[index]
	var offset int64
	if len(checkpoint.Manifest.Chunks) > 0 {
		offset = index * checkpoint.Manifest.Chunks[0].Bytes
	}
	data := buffer[:chunk.Bytes]
	if _, err := file.ReadAt(data, offset); err != nil {
		return ErrSourceChanged
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != string(chunk.SHA256) {
		return ErrSourceChanged
	}
	err := retryTransfer(ctx, func() error {
		response, e := c.do(ctx, call{method: "PUT", path: "/uploads/" + string(checkpoint.UploadID) + "/chunks/" + strconv.FormatInt(index, 10), token: c.token(), header: http.Header{api.ChunkDigestHeader: {string(chunk.SHA256)}, "Content-Type": {"application/octet-stream"}}, body: data})
		if e == nil {
			response.Body.Close()
		}
		return e
	})
	if err != nil {
		return err
	}
	checkpoint.acknowledge(index)
	if err = save(checkpoint); err != nil {
		return err
	}
	if progress != nil {
		progress(checkpoint.AcknowledgedBytes, checkpoint.Manifest.TotalBytes)
	}
	return nil
}

func (checkpoint *UploadCheckpoint) acknowledge(index int64) {
	for _, r := range checkpoint.Acknowledged {
		if index >= r.Start && index < r.End {
			return
		}
	}
	ranges := append(checkpoint.Acknowledged, api.ChunkRange{Start: index, End: index + 1})
	// Typical sequential work keeps a single range. Resumed holes can arrive
	// before existing acknowledgements; insert and merge without expansion.
	for i := len(ranges) - 1; i > 0 && ranges[i].Start < ranges[i-1].Start; i-- {
		ranges[i], ranges[i-1] = ranges[i-1], ranges[i]
	}
	merged := ranges[:0]
	for _, r := range ranges {
		if len(merged) > 0 && r.Start <= merged[len(merged)-1].End {
			merged[len(merged)-1].End = max(merged[len(merged)-1].End, r.End)
		} else {
			merged = append(merged, r)
		}
	}
	checkpoint.Acknowledged = merged
	checkpoint.AcknowledgedBytes += checkpoint.Manifest.Chunks[index].Bytes
}

func (checkpoint *UploadCheckpoint) validate(limits protocol.TransferLimits) error {
	manifest := checkpoint.Manifest
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	intent := checkpoint.Intent
	if intent.Validate(limits) != nil || checkpoint.SourceFingerprint.Validate() != nil || checkpoint.IdempotencyKey == "" || intent.FileID != manifest.FileID || intent.VersionID != manifest.VersionID || intent.Mode != manifest.Mode || intent.ChunkCount != int64(len(manifest.Chunks)) || intent.TransmittedBytes != manifest.TotalBytes || intent.ManifestBytes != int64(len(body)) || checkpoint.SourceSize != manifest.TotalBytes {
		return errors.New("invalid upload checkpoint intent or source fingerprint")
	}
	if checkpoint.UploadID != "" && checkpoint.UploadID.Validate() != nil {
		return errors.New("invalid checkpoint upload ID")
	}
	var count int64
	previousEnd := int64(-1)
	for _, r := range checkpoint.Acknowledged {
		if r.Start < 0 || r.Start >= r.End || r.End > int64(len(manifest.Chunks)) || r.Start <= previousEnd {
			return errors.New("invalid checkpoint acknowledgements")
		}
		for index := r.Start; index < r.End; index++ {
			count += manifest.Chunks[index].Bytes
		}
		previousEnd = r.End
	}
	if count != checkpoint.AcknowledgedBytes {
		return errors.New("invalid checkpoint acknowledged byte count")
	}
	return nil
}

func (c *Client) finalizeCheckpoint(ctx context.Context, checkpoint *UploadCheckpoint, save func(*UploadCheckpoint) error) (api.FinalizeResult, error) {
	body, err := json.Marshal(checkpoint.Manifest)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	var result api.FinalizeResult
	err = retryTransfer(ctx, func() error {
		response, e := c.do(ctx, call{method: "POST", path: "/uploads/" + string(checkpoint.UploadID) + "/finalize", token: c.token(), header: http.Header{api.IdempotencyKeyHeader: {checkpoint.IdempotencyKey}}, body: body, json: true})
		if e != nil {
			return e
		}
		defer response.Body.Close()
		result, e = api.DecodeFinalizeResult(response.Body)
		return e
	})
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if result.UploadID != checkpoint.UploadID || result.FileID != checkpoint.Manifest.FileID || result.VersionID != checkpoint.Manifest.VersionID {
		return api.FinalizeResult{}, errors.New("server finalization differs from persisted upload")
	}
	checkpoint.Result = &result
	checkpoint.Acknowledged = nil
	if len(checkpoint.Manifest.Chunks) > 0 {
		checkpoint.Acknowledged = []api.ChunkRange{{Start: 0, End: int64(len(checkpoint.Manifest.Chunks))}}
	}
	checkpoint.AcknowledgedBytes = checkpoint.Manifest.TotalBytes
	if err = save(checkpoint); err != nil {
		return api.FinalizeResult{}, err
	}
	return checkpointResult(checkpoint)
}

func checkpointResult(checkpoint *UploadCheckpoint) (api.FinalizeResult, error) {
	if checkpoint.Result == nil || checkpoint.Result.Validate() != nil || checkpoint.Result.UploadID != checkpoint.UploadID || checkpoint.Result.FileID != checkpoint.Manifest.FileID || checkpoint.Result.VersionID != checkpoint.Manifest.VersionID {
		return api.FinalizeResult{}, errors.New("invalid completed upload checkpoint")
	}
	if checkpoint.Result.State != protocol.UploadCommitted {
		return *checkpoint.Result, api.Error{Code: api.Conflict, Message: "The file changed on the server during upload; the upload was kept as a separate version"}
	}
	return *checkpoint.Result, nil
}

func openUploadSource(path string) (*os.File, os.FileInfo, error) {
	file, err := source.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, &source.SkipError{Path: path, Reason: "Directory skipped; use Upload folder to preserve its contents"}
	}
	if err := protocol.ValidateName(filepath.Base(path)); err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func (c *Client) admitCheckpoint(ctx context.Context, checkpoint *UploadCheckpoint, save func(*UploadCheckpoint) error) error {
	var upload api.Upload
	err := retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "POST", path: "/uploads", token: c.token(), header: http.Header{api.IdempotencyKeyHeader: {checkpoint.IdempotencyKey}}}, checkpoint.Intent, &upload)
	})
	if err != nil {
		return err
	}
	if upload.ID.Validate() != nil {
		return errors.New("server returned an invalid upload")
	}
	checkpoint.UploadID = upload.ID
	return save(checkpoint)
}

// CancelUploadCheckpoint resolves a lost admission response before cancellation.
// If publication won the race, Result is persisted and the caller reports its
// committed/conflicted outcome instead of claiming that server data was removed.
func (c *Client) CancelUploadCheckpoint(ctx context.Context, checkpoint *UploadCheckpoint, save func(*UploadCheckpoint) error) error {
	if checkpoint == nil || save == nil {
		return errors.New("upload checkpoint and persistence callback are required")
	}
	if checkpoint.Manifest.FileID == "" {
		return nil
	}
	account, signedIn := c.Account()
	if !signedIn {
		return api.Error{Code: api.AuthenticationRequired, Message: "Sign in before cancelling an upload"}
	}
	if checkpoint.Manifest.Scope != (protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}) {
		return errors.New("upload checkpoint belongs to another server or account")
	}
	if checkpoint.Result != nil {
		_, err := checkpointResult(checkpoint)
		var conflict api.Error
		if errors.As(err, &conflict) && conflict.Code == api.Conflict {
			return nil
		}
		return err
	}
	if checkpoint.UploadID == "" {
		limits, err := c.transferLimits(ctx)
		if err != nil {
			return err
		}
		if err = checkpoint.validate(limits); err != nil {
			return err
		}
		if err = c.admitCheckpoint(ctx, checkpoint, save); err != nil {
			return err
		}
	}
	err := c.CancelUpload(ctx, checkpoint.UploadID)
	var failure api.Error
	if errors.As(err, &failure) {
		if failure.Code == api.UploadGone || failure.Code == api.NotFound {
			return nil
		}
		if failure.Code == api.Conflict {
			_, reconcileErr := c.finalizeCheckpoint(ctx, checkpoint, save)
			if checkpoint.Result != nil {
				return nil
			}
			return reconcileErr
		}
	}
	return err
}

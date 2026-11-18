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
	"path/filepath"
	"strconv"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

// Small files are batched: per-file requests and server flushes, not bytes,
// dominate their cost. Batches stay well inside the request limit.
const (
	MaxBatchFileBytes = 1 << 20
	MaxBatchBodyBytes = 4 << 20
	MaxBatchFileCount = api.MaxBatchFiles
)

type BatchFile struct {
	Path   string
	Parent protocol.ID
}

type BatchOutcome struct {
	Result api.FinalizeResult
	Err    error
}

// SupportsBatch reports whether the server accepts batched small files.
func (c *Client) SupportsBatch(ctx context.Context) bool {
	if _, err := c.transferLimits(ctx); err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.batchUploads
}

// UploadBatch publishes small files with one request. Each source is read
// once into memory, then checked unchanged, like Upload. Outcomes follow the
// order of files; failures of one file do not affect the others. The error is
// for the request as a whole, when no file outcome is known.
func (c *Client) UploadBatch(ctx context.Context, files []BatchFile) ([]BatchOutcome, error) {
	account, signedIn := c.Account()
	if !signedIn {
		return nil, api.Error{Code: api.AuthenticationRequired, Message: "Sign in before uploading"}
	}
	if len(files) == 0 || len(files) > MaxBatchFileCount {
		return nil, errors.New("a batch holds 1 to 256 files")
	}
	limits, err := c.transferLimits(ctx)
	if err != nil {
		return nil, err
	}
	scope := protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}
	outcomes := make([]BatchOutcome, len(files))
	request := api.BatchUploadRequest{}
	sent := []int{}
	body := []byte{}
	for i, file := range files {
		entry, data, err := prepareBatchFile(file, scope, min(int64(MaxBatchFileBytes), limits.MaxChunkBytes))
		if err != nil {
			outcomes[i].Err = err
			continue
		}
		var manifest protocol.Manifest
		if err = json.Unmarshal(entry.Manifest, &manifest); err != nil {
			return nil, err
		}
		target, err := c.findUploadTarget(ctx, file.Parent, manifest.Standard.Name)
		if err != nil {
			return nil, err
		}
		if target != nil {
			manifest.FileID = target.ID
			entry.FileID = target.ID
			entry.ExpectedHead = target.HeadRevision
			entry.Manifest, _ = json.Marshal(manifest)
			entry.ManifestBytes = int64(len(entry.Manifest))
		}
		request.Files = append(request.Files, entry)
		body = append(body, data...)
		sent = append(sent, i)
	}
	if len(sent) == 0 {
		return outcomes, nil
	}
	header, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	call := call{method: "POST", path: api.BatchUploadPath, token: c.token(), body: append(header, body...), header: http.Header{
		api.BatchHeaderBytesHeader: {strconv.Itoa(len(header))},
		"Content-Type":             {"application/octet-stream"},
	}}
	var decoded api.BatchUploadResponse
	// Keep the prepared bytes and each per-file idempotency key unchanged,
	// including when the first response was lost after publication.
	err = retryTransfer(ctx, func() error {
		response, e := c.do(ctx, call)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		decoded, e = api.DecodeBatchUploadResponse(response.Body)
		return e
	})
	if err != nil {
		return nil, err
	}
	if decoded.Validate() != nil || len(decoded.Results) != len(sent) {
		return nil, errors.New("the server sent an invalid batch result")
	}
	for k, result := range decoded.Results {
		i, entry := sent[k], request.Files[k]
		switch {
		case result.Error != nil:
			outcomes[i].Err = *result.Error
		case result.Result.FileID != entry.FileID || result.Result.VersionID != entry.VersionID:
			return nil, errors.New("the server sent an invalid batch result")
		case result.Result.State != protocol.UploadCommitted:
			outcomes[i] = BatchOutcome{Result: *result.Result, Err: api.Error{Code: api.Conflict, Message: "The file changed on the server during upload; the upload was kept as a separate version"}}
		default:
			outcomes[i].Result = *result.Result
		}
	}
	return outcomes, nil
}

// prepareBatchFile applies Upload's source checks to a file of at most limit bytes.
func prepareBatchFile(file BatchFile, scope protocol.Scope, limit int64) (api.BatchUploadFile, []byte, error) {
	name := filepath.Base(file.Path)
	f, err := source.Open(file.Path)
	if err != nil {
		return api.BatchUploadFile{}, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return api.BatchUploadFile{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return api.BatchUploadFile{}, nil, &source.SkipError{Path: file.Path, Reason: "Directory skipped; use Upload folder to preserve its contents"}
	}
	if err := protocol.ValidateName(name); err != nil {
		return api.BatchUploadFile{}, nil, fmt.Errorf("%q cannot be used as a file name on the server", name)
	}
	if info.Size() > limit {
		return api.BatchUploadFile{}, nil, fmt.Errorf("%q is too large to batch", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, info.Size()+1))
	if err != nil {
		return api.BatchUploadFile{}, nil, err
	}
	current, err := f.Stat()
	if err != nil {
		return api.BatchUploadFile{}, nil, err
	}
	if int64(len(data)) != info.Size() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return api.BatchUploadFile{}, nil, ErrSourceChanged
	}
	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: scope, FileID: randomID(), VersionID: randomID(), Mode: protocol.Standard,
		Chunks: []protocol.Chunk{}, TotalBytes: int64(len(data)), Standard: &protocol.StandardMetadata{Name: name}}
	if len(data) > 0 {
		sum := sha256.Sum256(data)
		manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: 0, Bytes: int64(len(data)), SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return api.BatchUploadFile{}, nil, err
	}
	return api.BatchUploadFile{IdempotencyKey: string(randomID()), Manifest: body, CreateUploadRequest: api.CreateUploadRequest{
		ParentID: file.Parent, FileID: manifest.FileID, VersionID: manifest.VersionID, Mode: protocol.Standard, ExpectedHead: "0",
		ChunkCount: int64(len(manifest.Chunks)), TransmittedBytes: manifest.TotalBytes, ManifestBytes: int64(len(body)),
	}}, data, nil
}

package api

import (
	"errors"
	"io"

	"github.com/chironjit/serversend/shared/protocol"
)

// Batched small-file uploads publish many single-chunk files with one request
// and one metadata transaction. The request body is the JSON BatchUploadRequest
// of exactly BatchHeaderBytesHeader bytes, followed by each file's bytes in
// order. Every file keeps its own idempotency key, so a retried batch returns
// the recorded results of files that were already published.
const (
	FeatureBatchUpload     = "batch_upload"
	BatchUploadPath        = "/uploads/batch"
	BatchHeaderBytesHeader = "ServerSend-Batch-Header-Bytes"
	MaxBatchFiles          = 256
)

type BatchUploadFile struct {
	IdempotencyKey string `json:"idempotency_key"`
	CreateUploadRequest
	// Manifest holds the exact manifest bytes, as finalization would receive them.
	Manifest []byte `json:"manifest"`
}

type BatchUploadRequest struct {
	Files []BatchUploadFile `json:"files"`
}

// Validate checks framing only; each file's manifest is validated by the
// server individually so one bad file cannot reject the others.
func (b BatchUploadRequest) Validate(limits protocol.TransferLimits) error {
	if len(b.Files) == 0 || len(b.Files) > MaxBatchFiles {
		return errors.New("a batch holds 1 to 256 files")
	}
	keys := make(map[string]bool, len(b.Files))
	ids := make(map[protocol.ID]bool, len(b.Files))
	var total int64
	for _, file := range b.Files {
		if file.IdempotencyKey == "" || len(file.IdempotencyKey) > 128 || keys[file.IdempotencyKey] || ids[file.FileID] {
			return errors.New("each batched file needs a distinct idempotency key and file ID")
		}
		keys[file.IdempotencyKey], ids[file.FileID] = true, true
		if err := file.CreateUploadRequest.Validate(limits); err != nil {
			return err
		}
		if file.ChunkCount > 1 || int64(len(file.Manifest)) != file.ManifestBytes {
			return errors.New("batched files must be single-chunk with exact manifest lengths")
		}
		total += file.TransmittedBytes
		if total > limits.MaxRequestBytes {
			return errors.New("batch exceeds the server's request limit")
		}
	}
	return nil
}

func DecodeBatchUpload(r io.Reader, limit int64, limits protocol.TransferLimits) (BatchUploadRequest, error) {
	var value BatchUploadRequest
	if err := decode(r, min(limit, MaxControlBytes), &value); err != nil {
		return BatchUploadRequest{}, err
	}
	return value, value.Validate(limits)
}

// BatchUploadResult reports one file, in request order: exactly one of
// Result and Error is present.
type BatchUploadResult struct {
	Result *FinalizeResult `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type BatchUploadResponse struct {
	Results []BatchUploadResult `json:"results"`
}

func (b BatchUploadResponse) Validate() error {
	if len(b.Results) == 0 || len(b.Results) > MaxBatchFiles {
		return errors.New("invalid batch result count")
	}
	for _, result := range b.Results {
		if (result.Result == nil) == (result.Error == nil) {
			return errors.New("each batch result needs exactly one outcome")
		}
		if result.Result != nil {
			if err := result.Result.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func DecodeBatchUploadResponse(r io.Reader) (BatchUploadResponse, error) {
	return decodeValidated[BatchUploadResponse](r, MaxControlBytes)
}

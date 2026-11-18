package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
)

// Coalesce concurrently prepared uploads, keeping the durable queue's worker
// and memory limits. No temporary archive or additional source read is needed.
type smallUploadQueue struct {
	mu      sync.Mutex
	pending []*smallUpload
	bytes   int64
}
type smallUpload struct {
	ctx   context.Context
	entry api.BatchUploadFile
	data  []byte
	done  chan smallUploadResult
}
type smallUploadResult struct {
	result api.FinalizeResult
	err    error
}

func (c *Client) uploadSmallCheckpoint(ctx context.Context, file *os.File, info os.FileInfo, cp *UploadCheckpoint, save func(*UploadCheckpoint) error, progress Progress) (api.FinalizeResult, error) {
	data := make([]byte, cp.Manifest.TotalBytes)
	if len(data) > 0 {
		if _, err := file.ReadAt(data, 0); err != nil {
			return api.FinalizeResult{}, ErrSourceChanged
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != string(cp.Manifest.Chunks[0].SHA256) {
			return api.FinalizeResult{}, ErrSourceChanged
		}
	}
	current, err := file.Stat()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return api.FinalizeResult{}, ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return api.FinalizeResult{}, err
	}
	manifest, err := json.Marshal(cp.Manifest)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	item := &smallUpload{ctx: ctx, entry: api.BatchUploadFile{IdempotencyKey: cp.IdempotencyKey, Manifest: manifest, CreateUploadRequest: cp.Intent}, data: data, done: make(chan smallUploadResult, 1)}
	encoded, err := json.Marshal(item.entry)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	itemBytes := int64(len(encoded) + len(data) + 1)
	c.mu.Lock()
	budget := min(int64(MaxBatchBodyBytes), c.limits.MaxRequestBytes) - int64(len(`{"files":[]}`))
	c.mu.Unlock()
	q := &c.smallUploads
	q.mu.Lock()
	// The wire format requires distinct file IDs. Concurrent replacements of
	// the same target go in separate requests and retain conflict detection.
	duplicate := false
	for _, queued := range q.pending {
		if queued.entry.FileID == item.entry.FileID {
			duplicate = true
			break
		}
	}
	// Flush early when full; the timer for the old group becomes a no-op.
	if len(q.pending) > 0 && (len(q.pending) == MaxBatchFileCount || q.bytes+itemBytes > budget || duplicate) {
		pending := q.pending
		q.pending = nil
		q.bytes = 0
		go c.sendSmallUploads(pending)
	}
	q.pending = append(q.pending, item)
	q.bytes += itemBytes
	if len(q.pending) == 1 {
		first := item
		time.AfterFunc(2*time.Millisecond, func() {
			q.mu.Lock()
			var pending []*smallUpload
			if len(q.pending) > 0 && q.pending[0] == first {
				pending = q.pending
				q.pending = nil
				q.bytes = 0
			}
			q.mu.Unlock()
			if len(pending) > 0 {
				c.sendSmallUploads(pending)
			}
		})
	}
	q.mu.Unlock()
	// Once shared, wait for the actual outcome even after cancellation. This
	// ensures a committed response is checkpointed before releasing the worker.
	outcome := <-item.done
	if outcome.err != nil {
		return api.FinalizeResult{}, outcome.err
	}
	cp.UploadID = outcome.result.UploadID
	cp.Result = &outcome.result
	cp.Acknowledged = nil
	if len(cp.Manifest.Chunks) > 0 {
		cp.Acknowledged = []api.ChunkRange{{Start: 0, End: 1}}
	}
	cp.AcknowledgedBytes = cp.Manifest.TotalBytes
	if err := save(cp); err != nil {
		return api.FinalizeResult{}, err
	}
	if progress != nil {
		progress(cp.AcknowledgedBytes, cp.Manifest.TotalBytes)
	}
	return checkpointResult(cp)
}

func (c *Client) sendSmallUploads(pending []*smallUpload) {
	live := make([]*smallUpload, 0, len(pending))
	request := api.BatchUploadRequest{}
	for _, item := range pending {
		if err := item.ctx.Err(); err != nil {
			item.done <- smallUploadResult{err: err}
			continue
		}
		live = append(live, item)
		request.Files = append(request.Files, item.entry)
	}
	if len(live) == 0 {
		return
	}
	header, err := json.Marshal(request)
	var result api.BatchUploadResponse
	if err == nil {
		size := len(header)
		for _, item := range live {
			size += len(item.data)
		}
		body := make([]byte, 0, size)
		body = append(body, header...)
		for _, item := range live {
			body = append(body, item.data...)
		}
		// A single paused file must not cancel its neighbours. Bound shared work
		// independently; per-file pause/cancel still wins in the durable store.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(live[0].ctx), 30*time.Second)
		defer cancel()
		err = retryTransfer(ctx, func() error {
			response, e := c.do(ctx, call{method: "POST", path: api.BatchUploadPath, token: c.token(), body: body, header: http.Header{http.CanonicalHeaderKey(api.BatchHeaderBytesHeader): {strconv.Itoa(len(header))}, "Content-Type": {"application/octet-stream"}}})
			if e != nil {
				return e
			}
			defer response.Body.Close()
			result, e = api.DecodeBatchUploadResponse(response.Body)
			return e
		})
		if err == nil && len(result.Results) != len(live) {
			err = errors.New("the server sent an invalid batch result")
		}
		// Validate the entire response before exposing any of it to callers.
		if err == nil {
			for i, r := range result.Results {
				if r.Result != nil && (r.Result.FileID != live[i].entry.FileID || r.Result.VersionID != live[i].entry.VersionID) {
					err = errors.New("the server sent an invalid batch result")
					break
				}
			}
		}
	}
	for i, item := range live {
		outcome := smallUploadResult{err: err}
		if err == nil {
			r := result.Results[i]
			if r.Error != nil {
				outcome.err = *r.Error
			} else {
				outcome.result = *r.Result
			}
		}
		item.done <- outcome
	}
}

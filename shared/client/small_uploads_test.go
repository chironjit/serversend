package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestSmallUploadsCoalescePersistAndReplay(t *testing.T) {
	const count = 8
	var mu sync.Mutex
	saved := map[protocol.ID]bool{}
	requests := [][]byte{}
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/catalog/target") {
			return recoveryResponse(struct {
				Entry *api.CatalogEntry `json:"entry"`
			}{nil}), nil
		}
		if r.URL.Path != api.BasePath+api.BatchUploadPath {
			t.Errorf("unexpected request: %s", r.URL.Path)
			return nil, errors.New("unexpected request")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) > 8192 {
			t.Errorf("batch exceeded advertised request limit: %d", len(body))
		}
		n, _ := strconv.Atoi(r.Header.Get(api.BatchHeaderBytesHeader))
		var request api.BatchUploadRequest
		if err := json.Unmarshal(body[:n], &request); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, f := range request.Files {
			if !saved[f.FileID] {
				t.Error("sent before checkpoint persistence")
			}
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		response := api.BatchUploadResponse{}
		for _, f := range request.Files {
			response.Results = append(response.Results, api.BatchUploadResult{Result: &api.FinalizeResult{UploadID: randomID(), FileID: f.FileID, VersionID: f.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}})
		}
		return recoveryResponse(response), nil
	}))
	c.batchUploads = true
	c.limits.MaxRequestBytes = 8192
	c.limits.MaxManifestBytes = 8192
	ready := make(chan struct{}, count)
	release := make(chan struct{})
	var group sync.WaitGroup
	for i := range count {
		path := recoverySourcePath(t, strconv.Itoa(i))
		if err := os.WriteFile(path, bytes.Repeat([]byte("a"), 2048), 0600); err != nil {
			t.Fatal(err)
		}
		group.Go(func() {
			var cp UploadCheckpoint
			_, err := c.UploadResumable(t.Context(), path, "", &cp, func(cp *UploadCheckpoint) error {
				if cp.Manifest.FileID != "" && cp.Result == nil {
					mu.Lock()
					saved[cp.Manifest.FileID] = true
					mu.Unlock()
					ready <- struct{}{}
					<-release
				}
				return nil
			}, nil)
			if err != nil || cp.Result == nil || cp.AcknowledgedBytes != 2048 {
				t.Errorf("checkpoint: %+v, %v", cp, err)
			}
		})
	}
	for range count {
		<-ready
	}
	close(release)
	group.Wait()
	mu.Lock()
	defer mu.Unlock()
	// The failed first request is replayed with identical identities and bytes.
	repeats := false
	for _, body := range requests[1:] {
		if bytes.Equal(body, requests[0]) {
			repeats = true
		}
	}
	if !repeats {
		t.Fatal("lost response was not replayed unchanged")
	}
	if len(requests) >= count+1 {
		t.Fatalf("uploads did not coalesce: %d requests", len(requests))
	}
}

func TestSmallUploadPersistenceFailurePreventsMutation(t *testing.T) {
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("mutation after failed checkpoint")
		}
		return recoveryResponse(struct {
			Entry *api.CatalogEntry `json:"entry"`
		}{nil}), nil
	}))
	c.batchUploads = true
	path := recoverySourcePath(t, "small")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("disk full")
	var cp UploadCheckpoint
	_, err := c.UploadResumable(t.Context(), path, "", &cp, func(cp *UploadCheckpoint) error {
		if cp.Manifest.FileID != "" {
			return failed
		}
		return nil
	}, nil)
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
}

func TestSmallUploadCancellationDoesNotCancelNeighbours(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		cancel() // cancellation after sending must still record the response
		if r.Context().Err() != nil {
			t.Error("shared request inherited individual cancellation")
		}
		var request api.BatchUploadRequest
		n, _ := strconv.Atoi(r.Header.Get(api.BatchHeaderBytesHeader))
		if err := json.NewDecoder(io.LimitReader(r.Body, int64(n))).Decode(&request); err != nil {
			return nil, err
		}
		response := api.BatchUploadResponse{}
		for _, f := range request.Files {
			response.Results = append(response.Results, api.BatchUploadResult{Result: &api.FinalizeResult{UploadID: randomID(), FileID: f.FileID, VersionID: f.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}})
		}
		return recoveryResponse(response), nil
	}))
	cancelled, cancelBefore := context.WithCancel(t.Context())
	cancelBefore()
	makeItem := func(ctx context.Context) *smallUpload {
		return &smallUpload{ctx: ctx, entry: api.BatchUploadFile{CreateUploadRequest: api.CreateUploadRequest{FileID: randomID(), VersionID: randomID()}}, done: make(chan smallUploadResult, 1)}
	}
	a, b, d := makeItem(cancelled), makeItem(ctx), makeItem(t.Context())
	c.sendSmallUploads([]*smallUpload{a, b, d})
	if !errors.Is((<-a.done).err, context.Canceled) {
		t.Error("cancelled file was sent")
	}
	for _, item := range []*smallUpload{b, d} {
		if outcome := <-item.done; outcome.err != nil || outcome.result.UploadID == "" {
			t.Errorf("lost outcome: %+v", outcome)
		}
	}
}

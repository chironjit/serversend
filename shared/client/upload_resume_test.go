package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

type resumeFixture struct {
	t                  *testing.T
	intent             api.CreateUploadRequest
	uploadID           protocol.ID
	state              protocol.UploadState
	chunks             map[int64]bool
	puts               map[int64]int
	admissions         int
	cancelCount        int
	interruptChunk     int64
	interruptFinalize  bool
	interruptAdmission bool
	missingRangeLimit  int
	interrupted        bool
	saved              []byte
}

func (f *resumeFixture) save(checkpoint *UploadCheckpoint) error {
	var err error
	f.saved, err = json.Marshal(checkpoint)
	return err
}
func (f *resumeFixture) restored() UploadCheckpoint {
	var checkpoint UploadCheckpoint
	if err := json.Unmarshal(f.saved, &checkpoint); err != nil {
		f.t.Fatal(err)
	}
	return checkpoint
}
func (f *resumeFixture) client() *Client { return recoveryClient(sourceTestTransport(f.request)) }
func (f *resumeFixture) request(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	route := strings.TrimPrefix(r.URL.Path, api.BasePath)
	if route == "/catalog/target" {
		return recoveryResponse(struct {
			Entry *api.CatalogEntry `json:"entry"`
		}{nil}), nil
	}
	if route == "/uploads" && r.Method == "POST" {
		if len(f.saved) == 0 {
			f.t.Fatal("admission before persistence")
		}
		var cp UploadCheckpoint
		if err := json.Unmarshal(f.saved, &cp); err != nil {
			f.t.Fatal(err)
		}
		if cp.Manifest.FileID == "" || cp.IdempotencyKey != r.Header.Get(api.IdempotencyKeyHeader) {
			f.t.Fatal("admission identity not checkpointed")
		}
		var requested api.CreateUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
			f.t.Fatal(err)
		}
		if requested.FileID != f.intent.FileID {
			f.intent = requested
			f.admissions++
			f.uploadID = protocol.ID(fmt.Sprintf("%032x", f.admissions+10))
			f.state = protocol.UploadOpen
			f.chunks = map[int64]bool{}
		}
		if !f.interrupted && f.interruptAdmission {
			f.interrupted = true
			return nil, context.Canceled
		}
		return recoveryResponse(api.Upload{ID: f.uploadID, CreateUploadRequest: f.intent, State: f.state}), nil
	}
	if r.Method == "DELETE" {
		if f.state == protocol.UploadCommitted {
			response := recoveryResponse(api.ErrorResponse{Error: api.Error{Code: api.Conflict, Message: "Already finalized"}})
			response.StatusCode = http.StatusConflict
			return response, nil
		}
		f.cancelCount++
		f.state = protocol.UploadCancelled
		return recoveryResponse(nil), nil
	}
	if strings.HasSuffix(route, "/missing") {
		missing := api.MissingChunks{Page: api.Page{Snapshot: "55555555555555555555555555555555"}, UploadID: f.uploadID, ChunkCount: f.intent.ChunkCount, Missing: []api.ChunkRange{}}
		for i := int64(0); i < f.intent.ChunkCount; i++ {
			if !f.chunks[i] {
				if len(missing.Missing) > 0 && missing.Missing[len(missing.Missing)-1].End == i {
					missing.Missing[len(missing.Missing)-1].End++
				} else {
					missing.Missing = append(missing.Missing, api.ChunkRange{Start: i, End: i + 1})
				}
			}
		}
		if f.missingRangeLimit > 0 && len(missing.Missing) > f.missingRangeLimit {
			missing.Missing = missing.Missing[:f.missingRangeLimit]
		}
		return recoveryResponse(missing), nil
	}
	if strings.Contains(route, "/chunks/") {
		index, _ := strconv.ParseInt(route[strings.LastIndex(route, "/")+1:], 10, 64)
		f.puts[index]++
		f.chunks[index] = true
		_, _ = io.Copy(io.Discard, r.Body)
		if !f.interrupted && f.interruptChunk == index {
			f.interrupted = true
			return nil, context.Canceled
		}
		return recoveryResponse(nil), nil
	}
	if strings.HasSuffix(route, "/finalize") {
		f.state = protocol.UploadCommitted
		if !f.interrupted && f.interruptFinalize {
			f.interrupted = true
			return nil, context.Canceled
		}
		return recoveryResponse(api.FinalizeResult{UploadID: f.uploadID, FileID: f.intent.FileID, VersionID: f.intent.VersionID, State: f.state, CatalogRevision: "1", HeadRevision: "1"}), nil
	}
	if r.Method == "GET" {
		return recoveryResponse(api.Upload{ID: f.uploadID, CreateUploadRequest: f.intent, State: f.state}), nil
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, route)
	return nil, nil
}

func newResumeFixture(t *testing.T) (*resumeFixture, string) {
	t.Helper()
	path := recoverySourcePath(t, "source")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), 3*4096), 0600); err != nil {
		t.Fatal(err)
	}
	return &resumeFixture{t: t, puts: map[int64]int{}, interruptChunk: 1}, path
}

func TestUploadResumesMissingOnlyAfterLostAcknowledgement(t *testing.T) {
	f, path := newResumeFixture(t)
	var checkpoint UploadCheckpoint
	if _, err := f.client().UploadResumable(t.Context(), path, "", &checkpoint, f.save, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption: %v", err)
	}
	if f.cancelCount != 0 || checkpoint.UploadID == "" {
		t.Fatal("pause retired its upload")
	}
	persisted := f.restored()
	if persisted.AcknowledgedBytes != 4096 {
		t.Fatalf("persisted bytes=%d", persisted.AcknowledgedBytes)
	}
	result, err := f.client().UploadResumable(t.Context(), path, "", &persisted, f.save, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.UploadID != checkpoint.UploadID || f.admissions != 1 {
		t.Fatal("resume changed admission")
	}
	for i := int64(0); i < 3; i++ {
		if f.puts[i] != 1 {
			t.Fatalf("chunk %d transmitted %d times", i, f.puts[i])
		}
	}
}

func TestUploadReconcilesCommittedResultWithoutSource(t *testing.T) {
	f, path := newResumeFixture(t)
	f.interruptChunk = -1
	f.interruptFinalize = true
	var checkpoint UploadCheckpoint
	if _, err := f.client().UploadResumable(t.Context(), path, "", &checkpoint, f.save, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	persisted := f.restored()
	result, err := f.client().UploadResumable(t.Context(), path, "", &persisted, f.save, nil)
	if err != nil || result.State != protocol.UploadCommitted || f.admissions != 1 {
		t.Fatalf("committed reconciliation: %+v %v", result, err)
	}
}

func TestUploadChangedSourceStartsFreshGeneration(t *testing.T) {
	f, path := newResumeFixture(t)
	var checkpoint UploadCheckpoint
	if _, err := f.client().UploadResumable(t.Context(), path, "", &checkpoint, f.save, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	oldFile := checkpoint.Manifest.FileID
	if err := os.WriteFile(path, bytes.Repeat([]byte("b"), 2*4096), 0600); err != nil {
		t.Fatal(err)
	}
	persisted := f.restored()
	if _, err := f.client().UploadResumable(t.Context(), path, "", &persisted, f.save, nil); err != nil {
		t.Fatal(err)
	}
	if f.cancelCount != 1 || f.admissions != 2 || persisted.Manifest.FileID == oldFile {
		t.Fatalf("generation not retired: cancel=%d admits=%d", f.cancelCount, f.admissions)
	}
}

func TestUploadCheckpointFailureBlocksNetwork(t *testing.T) {
	_, path := newResumeFixture(t)
	sentinel := errors.New("database full")
	var checkpoint UploadCheckpoint
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("network after failed checkpoint")
		return nil, nil
	}))
	if _, err := c.UploadResumable(t.Context(), path, "", &checkpoint, func(*UploadCheckpoint) error { return sentinel }, nil); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestCancelUploadResolvesLostAdmissionAndCommitRace(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f, path := newResumeFixture(t)
			f.interruptAdmission = !committed
			f.interruptFinalize = committed
			f.interruptChunk = -1
			var cp UploadCheckpoint
			if _, err := f.client().UploadResumable(t.Context(), path, "", &cp, f.save, nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			restored := f.restored()
			if err := f.client().CancelUploadCheckpoint(t.Context(), &restored, f.save); err != nil {
				t.Fatal(err)
			}
			if f.admissions != 1 {
				t.Fatal("cancellation created another admission")
			}
			if committed && restored.Result == nil {
				t.Fatal("committed outcome not reconciled")
			}
			if !committed && f.cancelCount != 1 {
				t.Fatal("lost admission reservation not released")
			}
		})
	}
}

func TestUploadRequeriesTruncatedMissingRanges(t *testing.T) {
	f, path := newResumeFixture(t)
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), 7*4096), 0600); err != nil {
		t.Fatal(err)
	}
	var cp UploadCheckpoint
	if _, err := f.client().UploadResumable(t.Context(), path, "", &cp, f.save, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, index := range []int64{0, 2, 4, 6} {
		f.chunks[index] = true
	}
	f.missingRangeLimit = 1
	restored := f.restored()
	if _, err := f.client().UploadResumable(t.Context(), path, "", &restored, f.save, nil); err != nil {
		t.Fatal(err)
	}
	if f.puts[3] != 1 || f.puts[5] != 1 || f.puts[2] != 0 || f.puts[4] != 0 || f.puts[6] != 0 {
		t.Fatalf("missing range reconciliation: %v", f.puts)
	}
}

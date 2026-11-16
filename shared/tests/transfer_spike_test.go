package tests

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// This disposable in-memory rig exercises the wire contracts as an independent
// producer/consumer. It provides no authentication, persistence, capacity model,
// production retry policy, catalog CAS or durability guarantees. Its 8-KiB
// fixture and tiny negotiated chunks are deliberately unrelated to deployment
// performance. Do not turn this handler into a production storage service.
var spikeLimits = protocol.TransferLimits{
	MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536,
	MaxManifestBytes: 65536, MaxChunks: 8,
}

var spikeScope = protocol.Scope{
	ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222",
}

const (
	spikeFile        protocol.ID = "33333333333333333333333333333333"
	spikeVersion     protocol.ID = "44444444444444444444444444444444"
	spikeUpload      protocol.ID = "55555555555555555555555555555555"
	spikeCreateKey               = "66666666666666666666666666666666"
	spikeFinalizeKey             = "77777777777777777777777777777777"
)

type transferSpike struct {
	mu          sync.Mutex
	upload      api.Upload
	createKey   string
	finalizeKey string
	chunks      map[int64][]byte
	manifest    []byte
	result      *api.FinalizeResult
	snapshot    int
	receiving   chan struct{}
	interrupted chan struct{}
}

func spikeDigest(body []byte) protocol.Digest {
	sum := sha256.Sum256(body)
	return protocol.Digest(hex.EncodeToString(sum[:]))
}

func spikeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func spikeError(w http.ResponseWriter, code api.ErrorCode) {
	spikeJSON(w, code.HTTPStatus(), api.ErrorResponse{Error: api.Error{Code: code, Message: "Spike request rejected"}})
}

func (s *transferSpike) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	uploadPath := api.BasePath + "/uploads/" + string(spikeUpload)
	versionPath := api.BasePath + "/files/" + string(spikeFile) + "/versions/" + string(spikeVersion)
	mux.HandleFunc("POST "+api.BasePath+"/uploads", func(w http.ResponseWriter, r *http.Request) {
		intent, err := api.DecodeCreateUpload(r.Body, spikeLimits)
		if err != nil || protocol.ID(r.Header.Get(api.IdempotencyKeyHeader)).Validate() != nil {
			spikeError(w, api.InvalidRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.createKey != "" {
			if s.createKey != r.Header.Get(api.IdempotencyKeyHeader) || s.upload.CreateUploadRequest != intent {
				spikeError(w, api.Conflict)
				return
			}
			spikeJSON(w, 200, s.upload)
			return
		}
		s.createKey = r.Header.Get(api.IdempotencyKeyHeader)
		s.upload = api.Upload{ID: spikeUpload, CreateUploadRequest: intent, State: protocol.UploadOpen, ExpiresAt: time.Unix(1800003600, 0).UTC()}
		spikeJSON(w, 201, s.upload)
	})
	mux.HandleFunc("GET "+uploadPath, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		spikeJSON(w, 200, s.upload)
	})
	mux.HandleFunc("GET "+uploadPath+"/missing", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		missing := make([]api.ChunkRange, 0)
		for index := int64(0); index < s.upload.ChunkCount; {
			if _, found := s.chunks[index]; found {
				index++
				continue
			}
			start := index
			for index < s.upload.ChunkCount {
				if _, found := s.chunks[index]; found {
					break
				}
				index++
			}
			missing = append(missing, api.ChunkRange{Start: start, End: index})
		}
		s.snapshot++
		spikeJSON(w, 200, api.MissingChunks{
			Page:     api.Page{Snapshot: protocol.ID(fmt.Sprintf("%032x", s.snapshot))},
			UploadID: spikeUpload, ChunkCount: s.upload.ChunkCount, Missing: missing,
		})
	})
	mux.HandleFunc("PUT "+uploadPath+"/chunks/{index}", func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
		s.mu.Lock()
		count := s.upload.ChunkCount
		s.mu.Unlock()
		if err != nil || strconv.FormatInt(index, 10) != r.PathValue("index") || index < 0 || index >= count || spikeLimits.ValidateChunkSize(r.ContentLength, index == count-1) != nil {
			spikeError(w, api.InvalidRequest)
			return
		}
		select {
		case s.receiving <- struct{}{}:
		default:
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, spikeLimits.MaxChunkBytes+1))
		if err != nil || int64(len(body)) != r.ContentLength {
			select {
			case s.interrupted <- struct{}{}:
			default:
			}
			spikeError(w, api.InvalidRequest)
			return
		}
		if spikeDigest(body) != protocol.Digest(r.Header.Get(api.ChunkDigestHeader)) {
			spikeError(w, api.InvalidRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		status := 201
		if old, found := s.chunks[index]; found {
			if !bytes.Equal(old, body) {
				spikeError(w, api.Conflict)
				return
			}
			status = 200
		} else {
			s.chunks[index] = body
		}
		spikeJSON(w, status, api.ChunkAcknowledgement{UploadID: spikeUpload, Chunk: protocol.Chunk{Index: index, Bytes: int64(len(body)), SHA256: spikeDigest(body)}})
	})
	mux.HandleFunc("POST "+uploadPath+"/finalize", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, spikeLimits.MaxManifestBytes+1))
		if err != nil {
			spikeError(w, api.InvalidRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.result != nil {
			if s.finalizeKey != r.Header.Get(api.IdempotencyKeyHeader) || !bytes.Equal(body, s.manifest) {
				spikeError(w, api.Conflict)
				return
			}
			spikeJSON(w, 200, s.result)
			return
		}
		manifest, err := api.DecodeManifest(bytes.NewReader(body), spikeLimits, nil)
		if err != nil || api.ValidateManifestBodyLength(int64(len(body)), s.upload) != nil || protocol.ID(r.Header.Get(api.IdempotencyKeyHeader)).Validate() != nil {
			spikeError(w, api.InvalidRequest)
			return
		}
		if api.ValidateFinalize(manifest, s.upload, spikeScope, spikeLimits, nil) != nil {
			spikeError(w, api.InvalidRequest)
			return
		}
		for _, chunk := range manifest.Chunks {
			stored, found := s.chunks[chunk.Index]
			if !found || int64(len(stored)) != chunk.Bytes || spikeDigest(stored) != chunk.SHA256 {
				spikeError(w, api.Conflict)
				return
			}
		}
		s.upload.State = protocol.UploadCommitted
		s.manifest, s.finalizeKey = body, r.Header.Get(api.IdempotencyKeyHeader)
		s.result = &api.FinalizeResult{UploadID: spikeUpload, FileID: manifest.FileID, VersionID: manifest.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}
		spikeJSON(w, 200, s.result)
	})
	mux.HandleFunc("GET "+api.BasePath+"/files/"+string(spikeFile)+"/versions", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.result == nil {
			spikeError(w, api.NotFound)
			return
		}
		spikeJSON(w, 200, api.VersionPage{Page: api.Page{Snapshot: "88888888888888888888888888888888"}, FileID: spikeFile, Versions: []api.VersionInfo{{ID: spikeVersion, FileID: spikeFile, Mode: protocol.Standard, TransmittedBytes: s.upload.TransmittedBytes, ManifestSHA256: spikeDigest(s.manifest), CreatedAt: time.Unix(1800000000, 0).UTC()}}})
	})
	mux.HandleFunc("GET "+versionPath+"/manifest", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.result == nil {
			spikeError(w, api.NotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.manifest) // Preserve the exact representation, no encoder newline.
	})
	mux.HandleFunc("GET "+versionPath+"/chunks/{index}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
		body, found := s.chunks[index]
		if err != nil || !found || s.result == nil {
			spikeError(w, api.NotFound)
			return
		}
		if r.Header.Get("Range") != "" {
			spikeError(w, api.InvalidRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set(api.ChunkDigestHeader, string(spikeDigest(body)))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
			t.Error("spike must use verified TLS 1.3 and HTTP/2")
			w.WriteHeader(400)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func spikeRequest(t *testing.T, client *http.Client, method, url string, body []byte, key string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set(api.IdempotencyKeyHeader, key)
	}
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set(api.ChunkDigestHeader, string(spikeDigest(body)))
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 2 || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		response.Body.Close()
		t.Fatal("client did not negotiate TLS 1.3/HTTP2")
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func spikeRejected(t *testing.T, response *http.Response, code api.ErrorCode) {
	t.Helper()
	defer response.Body.Close()
	got, err := api.DecodeError(response.Body)
	if err != nil || response.StatusCode != code.HTTPStatus() || got.Code != code {
		t.Fatalf("expected %s rejection, got status=%d body=%+v error=%v", code, response.StatusCode, got, err)
	}
}

func spikeRead[T any](t *testing.T, response *http.Response, status int, decode func(io.Reader) (T, error)) T {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("unexpected status=%d body=%s", response.StatusCode, body)
	}
	value, err := decode(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func spikeInterrupt(t *testing.T, s *transferSpike, client *http.Client, url string, chunk []byte) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { reader.Close(); writer.Close() })
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = int64(len(chunk))
	request.Header.Set(api.ChunkDigestHeader, string(spikeDigest(chunk)))
	done := make(chan bool, 1)
	go func() {
		response, err := client.Do(request)
		acknowledged := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
		if response != nil {
			response.Body.Close()
		}
		done <- acknowledged
	}()
	if _, err := writer.Write(chunk[:len(chunk)/2]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.receiving:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start receiving the interrupted chunk")
	}
	_ = writer.CloseWithError(errors.New("injected source interruption"))
	select {
	case acknowledged := <-done:
		if acknowledged {
			t.Fatal("interrupted send unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted send did not finish")
	}
	select {
	case <-s.interrupted:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe the partial request body")
	}
}

func TestVerticalTransferSpike(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		size      int
		interrupt bool
	}{{"standard-empty", 0, false}, {"standard-multichunk", 8192 + 17, false}, {"interrupted-send", 8192 + 17, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			source := make([]byte, scenario.size)
			for i := range source {
				source[i] = byte(i * 31)
			}
			manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: spikeScope, FileID: spikeFile, VersionID: spikeVersion, Mode: protocol.Standard, TotalBytes: int64(len(source)), Chunks: []protocol.Chunk{}, Standard: &protocol.StandardMetadata{Name: "fixture.bin"}}
			chunks := make([][]byte, 0)
			for offset := 0; offset < len(source); offset += int(spikeLimits.MaxChunkBytes) {
				chunk := source[offset:min(offset+int(spikeLimits.MaxChunkBytes), len(source))]
				manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(len(chunks)), Bytes: int64(len(chunk)), SHA256: spikeDigest(chunk)})
				chunks = append(chunks, chunk)
			}
			// This representation must be retained unchanged through creation,
			// finalization, retries, manifest hashing and subsequent download.
			manifestBody, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			intent := api.CreateUploadRequest{FileID: spikeFile, VersionID: spikeVersion, Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: int64(len(chunks)), TransmittedBytes: int64(len(source)), ManifestBytes: int64(len(manifestBody))}
			createBody, err := json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			s := &transferSpike{chunks: make(map[int64][]byte), receiving: make(chan struct{}, 1), interrupted: make(chan struct{}, 1)}
			server := httptest.NewUnstartedServer(s.handler(t))
			server.EnableHTTP2 = true
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
			server.StartTLS()
			defer server.Close()
			client := server.Client() // Its test CA is trusted; verification stays enabled.
			client.Timeout = 5 * time.Second
			defer client.CloseIdleConnections()
			base := server.URL + api.BasePath
			uploadURL := base + "/uploads/" + string(spikeUpload)
			versionURL := base + "/files/" + string(spikeFile) + "/versions/" + string(spikeVersion)
			decodeUpload := func(r io.Reader) (api.Upload, error) { return api.DecodeUpload(r, spikeLimits) }
			upload := spikeRead(t, spikeRequest(t, client, "POST", base+"/uploads", createBody, spikeCreateKey), 201, decodeUpload)
			replayed := spikeRead(t, spikeRequest(t, client, "POST", base+"/uploads", createBody, spikeCreateKey), 200, decodeUpload)
			if upload.ID != spikeUpload || replayed != upload || upload.State != protocol.UploadOpen {
				t.Fatal("creation replay changed upload intent or identity")
			}
			spikeRejected(t, spikeRequest(t, client, "GET", versionURL+"/manifest", nil, ""), api.NotFound)
			missing := spikeRead(t, spikeRequest(t, client, "GET", uploadURL+"/missing", nil, ""), 200, api.DecodeMissingChunks)
			if missing.UploadID != upload.ID || missing.ChunkCount != int64(len(chunks)) || len(missing.Missing) != min(1, len(chunks)) {
				t.Fatal("missing snapshot does not bind to the created upload")
			}
			if len(chunks) > 0 {
				spikeRejected(t, spikeRequest(t, client, "POST", uploadURL+"/finalize", manifestBody, spikeFinalizeKey), api.Conflict)
			}
			if scenario.interrupt {
				spikeInterrupt(t, s, client, uploadURL+"/chunks/0", chunks[0])
				missing = spikeRead(t, spikeRequest(t, client, "GET", uploadURL+"/missing", nil, ""), 200, api.DecodeMissingChunks)
				if len(missing.Missing) != 1 || missing.Missing[0] != (api.ChunkRange{Start: 0, End: int64(len(chunks))}) {
					t.Fatal("partial send incorrectly became acknowledged work")
				}
			}
			// Reverse order also tests that manifest order is independent of
			// arrival order, including a final chunk below the minimum size.
			for index := len(chunks) - 1; index >= 0; index-- {
				url := uploadURL + "/chunks/" + strconv.Itoa(index)
				ack := spikeRead(t, spikeRequest(t, client, "PUT", url, chunks[index], ""), 201, api.DecodeChunkAcknowledgement)
				if ack.UploadID != upload.ID || ack.Chunk != manifest.Chunks[index] {
					t.Fatal("acknowledgement does not describe the transmitted bytes")
				}
				retry := spikeRead(t, spikeRequest(t, client, "PUT", url, chunks[index], ""), 200, api.DecodeChunkAcknowledgement)
				if retry != ack {
					t.Fatal("identical chunk retry changed acknowledgement")
				}
			}
			if len(chunks) > 0 {
				changed := bytes.Clone(chunks[0])
				changed[0] ^= 1
				spikeRejected(t, spikeRequest(t, client, "PUT", uploadURL+"/chunks/0", changed, ""), api.Conflict)
			}
			missing = spikeRead(t, spikeRequest(t, client, "GET", uploadURL+"/missing", nil, ""), 200, api.DecodeMissingChunks)
			if len(missing.Missing) != 0 {
				t.Fatal("accepted chunks remain missing")
			}
			// Valid JSON with one extra newline is still a different immutable
			// manifest intent, even when decoding yields the same Go value.
			spikeRejected(t, spikeRequest(t, client, "POST", uploadURL+"/finalize", append(bytes.Clone(manifestBody), '\n'), spikeFinalizeKey), api.InvalidRequest)
			result := spikeRead(t, spikeRequest(t, client, "POST", uploadURL+"/finalize", manifestBody, spikeFinalizeKey), 200, api.DecodeFinalizeResult)
			retried := spikeRead(t, spikeRequest(t, client, "POST", uploadURL+"/finalize", manifestBody, spikeFinalizeKey), 200, api.DecodeFinalizeResult)
			if result != retried || result.State != protocol.UploadCommitted {
				t.Fatal("finalization replay changed the committed result")
			}
			spikeRejected(t, spikeRequest(t, client, "POST", uploadURL+"/finalize", append(bytes.Clone(manifestBody), '\n'), spikeFinalizeKey), api.Conflict)
			committed := spikeRead(t, spikeRequest(t, client, "GET", uploadURL, nil, ""), 200, decodeUpload)
			if committed.State != protocol.UploadCommitted {
				t.Fatal("upload reconciliation lost the finalization result")
			}
			versions := spikeRead(t, spikeRequest(t, client, "GET", base+"/files/"+string(spikeFile)+"/versions", nil, ""), 200, api.DecodeVersionPage)
			response := spikeRequest(t, client, "GET", versionURL+"/manifest", nil, "")
			downloadedManifest, err := io.ReadAll(io.LimitReader(response.Body, spikeLimits.MaxManifestBytes+1))
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || len(versions.Versions) != 1 || spikeDigest(downloadedManifest) != versions.Versions[0].ManifestSHA256 || !bytes.Equal(downloadedManifest, manifestBody) {
				t.Fatal("manifest bytes do not match the immutable version digest")
			}
			decoded, err := api.DecodeManifest(bytes.NewReader(downloadedManifest), spikeLimits, nil)
			if err != nil {
				t.Fatal(err)
			}
			var restored bytes.Buffer
			for _, chunk := range decoded.Chunks {
				response := spikeRequest(t, client, "GET", versionURL+"/chunks/"+strconv.FormatInt(chunk.Index, 10), nil, "")
				body, err := io.ReadAll(io.LimitReader(response.Body, spikeLimits.MaxChunkBytes+1))
				response.Body.Close()
				if err != nil || response.StatusCode != 200 || response.ContentLength != chunk.Bytes || int64(len(body)) != chunk.Bytes || spikeDigest(body) != chunk.SHA256 || response.Header.Get(api.ChunkDigestHeader) != string(chunk.SHA256) {
					t.Fatal("downloaded object does not match manifest length and digest")
				}
				restored.Write(body)
			}
			if !bytes.Equal(restored.Bytes(), source) {
				t.Fatal("restored file does not match the original source")
			}
		})
	}
}

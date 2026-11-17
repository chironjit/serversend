package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func recoveryClient(transport http.RoundTripper) *Client {
	return &Client{origin: "https://example.test", http: &http.Client{Transport: transport}, session: api.Session{
		ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "credential",
	}, limits: protocol.TransferLimits{MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536, MaxManifestBytes: 65536, MaxChunks: 100}}
}
func recoveryResponse(value any) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
}
func TestUploadRetriesKeepIntentAndChunkBytes(t *testing.T) {
	path := recoverySourcePath(t, "source")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), 8192), 0600); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	bodies, keys := map[string][]byte{}, map[string]string{}
	var intent api.CreateUploadRequest
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		route := r.URL.Path
		counts[route]++
		if counts[route] == 1 {
			bodies[route], keys[route] = body, r.Header.Get(api.IdempotencyKeyHeader)
			return nil, io.ErrUnexpectedEOF
		}
		if !bytes.Equal(bodies[route], body) || keys[route] != r.Header.Get(api.IdempotencyKeyHeader) {
			t.Fatalf("replay changed intent: %s", route)
		}
		switch {
		case route == api.BasePath+"/uploads":
			if err := json.Unmarshal(body, &intent); err != nil {
				t.Fatal(err)
			}
			return recoveryResponse(api.Upload{ID: "33333333333333333333333333333333"}), nil
		case strings.HasSuffix(route, "/finalize"):
			return recoveryResponse(api.FinalizeResult{UploadID: "33333333333333333333333333333333", FileID: intent.FileID, VersionID: intent.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}), nil
		case r.Method == "PUT":
			return recoveryResponse(nil), nil
		default:
			t.Fatalf("unexpected request: %s", route)
			return nil, nil
		}
	}))
	if _, err := c.Upload(t.Context(), path, nil); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 4 {
		t.Fatalf("requests: %v", counts)
	}
	for route, n := range counts {
		if n != 2 {
			t.Fatalf("%s attempts=%d", route, n)
		}
	}
}
func TestDirectoryRetryKeepsRequest(t *testing.T) {
	var original []byte
	calls := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if calls == 1 {
			original = body
			return nil, io.ErrUnexpectedEOF
		}
		if !bytes.Equal(original, body) {
			t.Fatal("directory identity changed")
		}
		return recoveryResponse(api.CatalogEntry{ID: "33333333333333333333333333333333", Kind: api.Directory, Mode: protocol.Standard, HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: "folder"}}), nil
	}))
	if _, err := c.CreateDirectory(t.Context(), "", "folder"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
func downloadFixture() (api.CatalogEntry, protocol.Manifest, []byte) {
	data := append(bytes.Repeat([]byte("a"), 4096), bytes.Repeat([]byte("b"), 4096)...)
	entry := api.CatalogEntry{ID: "33333333333333333333333333333333", HeadVersionID: "44444444444444444444444444444444"}
	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: protocol.Scope{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222"}, FileID: entry.ID, VersionID: entry.HeadVersionID, Mode: protocol.Standard, Standard: &protocol.StandardMetadata{Name: "file"}, TotalBytes: int64(len(data))}
	for i := 0; i < 2; i++ {
		sum := sha256.Sum256(data[i*4096 : (i+1)*4096])
		manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(i), Bytes: 4096, SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
	}
	return entry, manifest, data
}
func TestDownloadResumesVerifiedPrefixWithNewClient(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "intact", true: "corrupt checkpoint"}[corrupt], func(t *testing.T) {
			entry, manifest, data := downloadFixture()
			destination := filepath.Join(t.TempDir(), "restored")
			if err := os.WriteFile(destination, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			chunks := map[string]int{}
			transport := sourceTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/manifest") {
					return recoveryResponse(manifest), nil
				}
				chunks[r.URL.Path]++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			err := recoveryClient(transport).Download(ctx, entry, destination, func(done, total int64) {
				if done == 4096 {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("interruption: %v", err)
			}
			existing, _ := os.ReadFile(destination)
			if string(existing) != "existing" {
				t.Fatal("partial replaced destination")
			}
			partials, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".serversend-*.partial"))
			if len(partials) != 1 {
				t.Fatalf("checkpoints: %v", partials)
			}
			if corrupt {
				if err := os.WriteFile(partials[0], bytes.Repeat([]byte("x"), 4096), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoveryClient(transport).Download(t.Context(), entry, destination, nil); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(destination)
			if !bytes.Equal(got, data) {
				t.Fatal("restore differs")
			}
			first := api.BasePath + "/files/" + string(entry.ID) + "/versions/" + string(entry.HeadVersionID) + "/chunks/0"
			want := 1
			if corrupt {
				want = 2
			}
			if chunks[first] != want {
				t.Fatalf("first chunk fetched %d times, want %d", chunks[first], want)
			}
			if _, err := os.Stat(partials[0]); !os.IsNotExist(err) {
				t.Fatalf("checkpoint retained: %v", err)
			}
		})
	}
}
func TestTransferRetryClassificationAndCancellation(t *testing.T) {
	for _, err := range []error{ErrIdentityChanged, context.Canceled, ErrSourceChanged, api.Error{Code: api.AuthenticationRequired}, api.Error{Code: api.CapacityExhausted}, &url.Error{Op: "Post", URL: "https://example.test", Err: x509.UnknownAuthorityError{}}} {
		calls := 0
		got := retryTransfer(t.Context(), func() error { calls++; return err })
		if calls != 1 || got != err {
			t.Fatalf("retried terminal error %v: %d", err, calls)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := retryTransfer(ctx, func() error { calls++; cancel(); return io.ErrUnexpectedEOF })
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry: %d %v", calls, err)
	}
}

func TestDownloadRetriesInterruptedChunkBody(t *testing.T) {
	entry, manifest, data := downloadFixture()
	attempts := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			return recoveryResponse(manifest), nil
		}
		attempts++
		body := recoveryChunk(r, data)
		if attempts == 1 {
			body = body[:100]
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}))
	destination := filepath.Join(t.TempDir(), "restored")
	if err := c.Download(t.Context(), entry, destination, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(destination)
	if !bytes.Equal(got, data) || attempts != 3 {
		t.Fatalf("chunk recovery: attempts=%d bytes=%d", attempts, len(got))
	}
}

func TestDownloadCheckpointLinksNeverModifyTarget(t *testing.T) {
	for _, symbolic := range []bool{false, true} {
		t.Run(map[bool]string{false: "hardlink", true: "symlink"}[symbolic], func(t *testing.T) {
			entry, manifest, data := downloadFixture()
			destination := filepath.Join(t.TempDir(), "restored")
			c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/manifest") {
					return recoveryResponse(manifest), nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(recoveryChunk(r, data)))}, nil
			}))
			ctx, cancel := context.WithCancel(t.Context())
			if err := c.Download(ctx, entry, destination, func(done, total int64) {
				if done == 4096 {
					cancel()
				}
			}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			partials, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".serversend-*.partial"))
			if len(partials) != 1 {
				t.Fatal(partials)
			}
			target := filepath.Join(t.TempDir(), "untouched")
			original := []byte("never change this file")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(partials[0]); err != nil {
				t.Fatal(err)
			}
			link := os.Link
			if symbolic {
				link = os.Symlink
			}
			if err := link(target, partials[0]); err != nil {
				t.Skipf("link unavailable: %v", err)
			}
			err := c.Download(t.Context(), entry, destination, nil)
			if symbolic && err == nil {
				t.Fatal("symlink checkpoint accepted")
			}
			if !symbolic && err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(target)
			if !bytes.Equal(got, original) {
				t.Fatal("checkpoint target modified")
			}
		})
	}
}

func TestBatchRetriesKeepPreparedBytes(t *testing.T) {
	path := recoverySourcePath(t, "small")
	if err := os.WriteFile(path, []byte("small file"), 0600); err != nil {
		t.Fatal(err)
	}
	var original []byte
	attempts := 0
	c := recoveryClient(sourceTestTransport(func(r *http.Request) (*http.Response, error) {
		attempts++
		body, _ := io.ReadAll(r.Body)
		if attempts == 1 {
			original = body
			return nil, io.ErrUnexpectedEOF
		}
		if !bytes.Equal(original, body) {
			t.Fatal("batch identity or bytes changed")
		}
		// The header is the first JSON value, followed by raw file bytes.
		var request api.BatchUploadRequest
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&request); err != nil {
			t.Fatal(err)
		}
		f := request.Files[0]
		return recoveryResponse(api.BatchUploadResponse{Results: []api.BatchUploadResult{{Result: &api.FinalizeResult{UploadID: "33333333333333333333333333333333", FileID: f.FileID, VersionID: f.VersionID, State: protocol.UploadCommitted, CatalogRevision: "1", HeadRevision: "1"}}}}), nil
	}))
	outcomes, err := c.UploadBatch(t.Context(), []BatchFile{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("batch recovery: %d %v", attempts, outcomes)
	}
}

func TestDirectoryRecoversFromClosedQUICConnection(t *testing.T) {
	var creates, finalProtocol atomic.Int64
	active := make(chan http.RoundTripper, 1)
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			return
		}
		// Model a server-side directory commit followed by connection loss before
		// its response. The replay receives that same directory over HTTP/2.
		if creates.Add(1) == 1 {
			closeConnection(<-active)
			return
		}
		finalProtocol.Store(int64(r.ProtoMajor))
		_ = json.NewEncoder(w).Encode(api.CatalogEntry{ID: "33333333333333333333333333333333", Kind: api.Directory, Mode: protocol.Standard, HeadRevision: "1", Standard: &protocol.StandardMetadata{Name: "folder"}})
	}), true)
	c, err := New(server.URL, fingerprint(server.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	conn, err := c.http.Transport.(*adaptiveTransport).connection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	active <- conn
	if _, err := c.CreateDirectory(t.Context(), "", "folder"); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 2 || finalProtocol.Load() != 2 {
		t.Fatalf("creates=%d final protocol=%d", creates.Load(), finalProtocol.Load())
	}
}

func recoveryChunk(r *http.Request, data []byte) []byte {
	if strings.HasSuffix(r.URL.Path, "/chunks/1") {
		return data[4096:]
	}
	return data[:4096]
}

func recoverySourcePath(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, name)
}

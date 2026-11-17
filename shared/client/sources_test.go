package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

type sourceRequestGuard struct{ t *testing.T }

func (g sourceRequestGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	g.t.Fatalf("skipped source reached network: %s %s", r.Method, r.URL)
	return nil, nil
}

type sourceTestTransport func(*http.Request) (*http.Response, error)

func (f sourceTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceChangesBeforeHashingAbortBeforeUploadCreation(t *testing.T) {
	for _, test := range []struct {
		name             string
		initial, changed int
	}{{"grow", 1024, 8192}, {"shrink", 1024, 512}, {"empty grows", 0, 1}, {"same size changes", 1024, 1024}} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "source")
			if err := os.WriteFile(path, bytes.Repeat([]byte("a"), test.initial), 0600); err != nil {
				t.Fatal(err)
			}
			initial, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			transport := sourceTestTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Path != api.BasePath+"/capabilities" {
					t.Fatalf("changed source created upload: %s", r.URL.Path)
				}
				if err := os.WriteFile(path, bytes.Repeat([]byte("b"), test.changed), 0600); err != nil {
					t.Fatal(err)
				}
				// Make the timestamp distinction independent of filesystem clock resolution.
				changedAt := initial.ModTime().Add(time.Second)
				if err := os.Chtimes(path, changedAt, changedAt); err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(api.Capabilities{ProtocolVersions: []protocol.Version{protocol.Version1}, Transports: []api.Transport{api.HTTP2}, Limits: protocol.TransferLimits{MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536, MaxManifestBytes: 65536, MaxChunks: 100}})
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			c := &Client{origin: "https://127.0.0.1:58443", http: &http.Client{Transport: transport}, session: api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "credential"}}
			if _, err := c.Upload(t.Context(), path, nil); !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("source change result=%v", err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d", requests)
			}
		})
	}
}

func TestDirectUploadRejectsLinksBeforeUploadMutation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(root, "ordinary")
	if err := os.WriteFile(regular, []byte("must not upload through link"), 0600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "link")
	if err := os.Symlink(regular, linked); err != nil {
		t.Skipf("native symlink fixture unavailable: %v", err)
	}
	c := &Client{origin: "https://127.0.0.1:58443", http: &http.Client{Transport: sourceRequestGuard{t}}, session: api.Session{ServerID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222", AccessToken: "credential"}, limits: protocol.TransferLimits{MinChunkBytes: 4096, MaxChunkBytes: 4096, MaxRequestBytes: 65536, MaxManifestBytes: 65536, MaxChunks: 100}}
	if _, err := c.Upload(t.Context(), linked, nil); err == nil {
		t.Fatal("link upload succeeded")
	} else if skip, ok := source.Skipped(err); !ok || skip.Reason != source.LinkReason {
		t.Fatalf("link upload error=%v", err)
	}
	if _, err := c.Upload(t.Context(), root, nil); err == nil {
		t.Fatal("directory direct upload succeeded")
	} else if _, ok := source.Skipped(err); !ok {
		t.Fatalf("directory upload error=%v", err)
	}
}

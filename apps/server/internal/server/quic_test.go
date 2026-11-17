package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/quic-go/quic-go/http3"
)

func startDualServer(t testing.TB, s *Server, handler http.Handler) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		defer tcp.Close()
		defer udp.Close()
		done <- s.serveTransports(ctx, cancel, tcp, udp, handler)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("dual transport shutdown did not complete")
		}
	})
	return "https://" + tcp.Addr().String()
}

func TestHTTP3StandardTransferWithSameAuthenticatedAPI(t *testing.T) {
	cfg := fixture(t)
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	apiHandler := httpapi.New(s.ready, httpapi.WithAuthentication(s.accounts), httpapi.WithTransfers(s.transfers), httpapi.WithHTTP3())
	var h3Requests atomic.Int64
	origin := startDualServer(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 3 {
			h3Requests.Add(1)
			if r.TLS == nil || !r.TLS.HandshakeComplete {
				t.Error("API received early data")
			}
		}
		apiHandler.ServeHTTP(w, r)
	}))
	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := onboardedTransportClient(t, origin, strings.ReplaceAll(s.CertificateFingerprint(), " ", ""), "admin", issued.TemporaryPassword, client.TransportOptions{PreferHTTP3: true})
	// The benchmark/test account is a normal user for transfer authorization.
	if _, err := s.db.Exec(`UPDATE accounts SET role='user' WHERE username='admin'`); err != nil {
		t.Fatal(err)
	}
	path, contents := writeFixture(t, t.TempDir(), "quic file.bin", 9<<20+17)
	if _, err := c.Upload(t.Context(), path, nil); err != nil {
		t.Fatal(err)
	}
	page, err := c.Catalog(t.Context())
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("catalog: %+v %v", page, err)
	}
	destination := filepath.Join(t.TempDir(), "restored.bin")
	if err := c.Download(t.Context(), page.Entries[0], destination, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(restored, contents) {
		t.Fatalf("restore mismatch: %v", err)
	}
	if err := c.SignOut(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Catalog(t.Context()); err == nil {
		t.Fatal("signed out QUIC request was authorized")
	}
	if h3Requests.Load() < 15 {
		t.Fatalf("workflow failed to use HTTP3: %d requests", h3Requests.Load())
	}
	response, err := testClient(t, cfg).Get(origin + api.BasePath + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var capabilities api.Capabilities
	if err := json.NewDecoder(response.Body).Decode(&capabilities); err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 2 || len(capabilities.Transports) != 2 || capabilities.Transports[1] != api.HTTP3 {
		t.Fatalf("TCP capabilities: %+v %s", capabilities, response.Proto)
	}
}

func TestQUICFailureKeepsHTTP2Available(t *testing.T) {
	cfg := fixture(t)
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	_ = udp.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.ServeTransports(ctx, tcp, udp) }()
	response, err := testClient(t, cfg).Get("https://" + tcp.Addr().String() + api.BasePath + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.ProtoMajor != 2 || response.StatusCode != http.StatusOK {
		t.Fatalf("fallback: %s status %d", response.Proto, response.StatusCode)
	}
	select {
	case err := <-done:
		t.Fatalf("QUIC failure stopped TCP: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fallback shutdown did not finish")
	}
}

func TestDualTransportCancellationDrainsQUICRequest(t *testing.T) {
	s, err := New(t.Context(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.serveTransports(ctx, cancel, tcp, udp, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				return
			}
			if r.ProtoMajor != 3 {
				t.Error("drain fixture did not use HTTP/3")
			}
			close(entered)
			select {
			case <-release:
				_, _ = io.WriteString(w, `{"challenge":"ok","next_step":"second_factor"}`)
			case <-r.Context().Done():
				t.Error("QUIC request cancelled before drain")
			}
		}))
	}()
	c, err := client.NewWithOptions("https://"+tcp.Addr().String(), strings.ReplaceAll(s.CertificateFingerprint(), " ", ""), client.TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	requestDone := make(chan error, 1)
	requestCtx, requestCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer requestCancel()
	go func() { _, err := c.Password(requestCtx, "user", "secret"); requestDone <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("QUIC handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server stopped before request drain: %v", err)
	default:
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("QUIC drain did not finish")
	}
}

func TestQUICStalledRequestBodyHasReadDeadline(t *testing.T) {
	cfg := fixture(t)
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	readDone := make(chan error, 1)
	origin := startDualServer(t, s, requestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		readDone <- err
	}), 20*time.Millisecond))
	verifiedTLS := testClient(t, cfg).Transport.(*http.Transport).TLSClientConfig.Clone()
	transport := &http3.Transport{TLSClientConfig: verifiedTLS}
	t.Cleanup(func() { _ = transport.Close() })
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, origin, reader)
	if err != nil {
		t.Fatal(err)
	}
	r.ContentLength = 1024
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := (&http.Client{Transport: transport}).Do(r)
		if response != nil {
			response.Body.Close()
		}
	}()
	go func() { _, _ = writer.Write([]byte("partial")) }()
	select {
	case err := <-readDone:
		if err == nil || !os.IsTimeout(err) {
			t.Fatalf("stalled QUIC body read: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled QUIC request body was not bounded")
	}
	cancel()
	writer.Close()
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled upload did not stop")
	}
}

func TestTCPFailureStopsQUICListener(t *testing.T) {
	s, err := New(t.Context(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	_ = tcp.Close()
	done := make(chan error, 1)
	go func() { done <- s.ServeTransports(t.Context(), tcp, udp) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("TCP failure was swallowed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TCP failure left QUIC running")
	}
	if _, err := udp.WriteTo([]byte("closed"), udp.LocalAddr()); err == nil {
		t.Fatal("UDP socket leaked")
	}
}

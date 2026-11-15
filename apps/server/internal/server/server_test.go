package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
)

func fixture(t *testing.T) config.Config {
	t.Helper()
	c := config.Defaults()
	root := t.TempDir()
	c.ConfigDir, c.DataDir, c.StagingDir = filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "staging")
	for _, dir := range []string{c.ConfigDir, c.DataDir, c.StagingDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	c.TLSCertFile, c.TLSKeyFile = filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
	writeCertificate(t, c, nil)
	return c
}

func writeCertificate(t *testing.T, c config.Config, change func(*x509.Certificate)) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ServerSend test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	if change != nil {
		change(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	for filename, block := range map[string]*pem.Block{
		c.TLSCertFile: {Type: "CERTIFICATE", Bytes: der},
		c.TLSKeyFile:  {Type: "PRIVATE KEY", Bytes: key},
	} {
		if err := os.WriteFile(filename, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func testClient(t *testing.T, c config.Config) *http.Client {
	t.Helper()
	cert, err := os.ReadFile(c.TLSCertFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid test certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func TestTLSHTTP2AndRestart(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id := s.Identity()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	client := testClient(t, c)
	base := "https://" + listener.Addr().String()
	for _, path := range []string{"/healthz", "/readyz", api.BasePath + "/capabilities"} {
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || response.ProtoMajor != 2 || response.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("HTTPS endpoint: status=%d protocol=%s TLS=%x", response.StatusCode, response.Proto, response.TLS.Version)
		}
		if strings.HasSuffix(path, "/capabilities") {
			if _, err := api.DecodeCapabilities(response.Body); err != nil {
				t.Fatal(err)
			}
		}
		response.Body.Close()
	}
	// A plaintext request may receive Go's TLS-required error, but never an
	// application success response or protocol metadata.
	plain := &http.Client{Timeout: time.Second}
	if response, err := plain.Get("http://" + listener.Addr().String() + "/healthz"); err == nil {
		response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatal("application served over plaintext")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
	if err := s.ready(context.Background()); err == nil {
		t.Fatal("draining server reported ready")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Ordinary TLS renewal must not replace the independent server identity.
	writeCertificate(t, c, nil)
	restarted, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Identity() != id {
		t.Fatal("restart or certificate replacement changed identity")
	}
	restarted.db.Close()
	if err := restarted.ready(t.Context()); err == nil {
		t.Fatal("closed metadata database reported ready")
	}
}

func TestInvalidTLSFailsBeforeMetadataCreation(t *testing.T) {
	for _, scenario := range []string{"unprovisioned", "missing key", "malformed", "mismatched key", "expired", "future", "client only"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixture(t)
			switch scenario {
			case "unprovisioned":
				c.TLSCertFile, c.TLSKeyFile = "", ""
			case "missing key":
				if err := os.Remove(c.TLSKeyFile); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(c.TLSCertFile, []byte("invalid certificate"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mismatched key":
				other := fixture(t)
				c.TLSKeyFile = other.TLSKeyFile
			case "expired":
				writeCertificate(t, c, func(cert *x509.Certificate) { cert.NotAfter = time.Now().Add(-time.Minute) })
			case "future":
				writeCertificate(t, c, func(cert *x509.Certificate) { cert.NotBefore = time.Now().Add(time.Minute) })
			case "client only":
				writeCertificate(t, c, func(cert *x509.Certificate) { cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} })
			}
			if s, err := New(t.Context(), c); err == nil || s != nil {
				t.Fatalf("invalid TLS accepted: server=%v error=%v", s, err)
			}
			if _, err := os.Stat(filepath.Join(c.ConfigDir, database.Filename)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid provisioning mutated metadata: %v", err)
			}
		})
	}
}

func TestGracefulShutdownDrainsActiveRequest(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- s.serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-release:
				_, _ = io.WriteString(w, "completed")
			case <-r.Context().Done():
				t.Error("active request was cancelled before drain completed")
			}
		}))
	}()
	requestDone := make(chan error, 1)
	client := testClient(t, c)
	go func() {
		response, err := client.Get("https://" + listener.Addr().String())
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			err = readErr
			if string(body) != "completed" {
				err = errors.New("incomplete response")
			}
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached handler")
	}
	cancel()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !s.draining.Load() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("shutdown did not mark readiness unavailable")
		}
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown completed before active request: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active request did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete after request drained")
	}
}

func TestServeCancellationAndListenerFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed listener", true: "cancelled context"}[cancelled], func(t *testing.T) {
			c := fixture(t)
			s, err := New(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			} else {
				listener.Close()
			}
			if err := s.Serve(ctx, listener); err == nil {
				t.Fatal("startup failure was ignored")
			}
			if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("listener was not closed: %v", err)
			}
		})
	}
}

func TestDrainDeadlineClosesActiveRequest(t *testing.T) {
	c := fixture(t)
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.drainTimeout = 20 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, handlerDone := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(handlerDone)
		}))
	}()
	requestDone := make(chan error, 1)
	client := testClient(t, c)
	go func() {
		response, err := client.Get("https://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached handler")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown deadline not reported: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadline did not bound the drain")
	}
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("forced shutdown did not cancel active request")
	}
	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("forced closure returned a successful response")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forced shutdown did not close client connection")
	}
}

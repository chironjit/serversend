package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/localtls"
)

func TestPrivateIPv4TLSHTTP2AndRestart(t *testing.T) {
	c := fixture(t)
	c.TLSCertFile, c.TLSKeyFile = "", ""
	c.TLSIPs = "127.0.0.1,192.168.1.20"
	s, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	caPath := filepath.Join(c.ConfigDir, localtls.Directory, localtls.CAFile)
	public, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(public) {
		t.Fatal("invalid generated CA")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	url := "https://" + listener.Addr().String() + "/readyz"
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.ProtoMajor != 2 || response.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("status=%d HTTP=%s", response.StatusCode, response.Proto)
	}
	// A client with no provisioned anchor must reject the private certificate.
	untrustedTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}
	defer untrustedTransport.CloseIdleConnections()
	if response, err := (&http.Client{Transport: untrustedTransport, Timeout: 5 * time.Second}).Get(url); err == nil {
		response.Body.Close()
		t.Fatal("untrusted private certificate accepted")
	}
	wrongIPTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "192.168.1.21"}}
	defer wrongIPTransport.CloseIdleConnections()
	if response, err := (&http.Client{Transport: wrongIPTransport, Timeout: 5 * time.Second}).Get(url); err == nil {
		response.Body.Close()
		t.Fatal("wrong IP accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
	identity := s.Identity()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Identity() != identity {
		t.Fatal("private certificate restart replaced server identity")
	}
	rootAfter, err := os.ReadFile(caPath)
	if err != nil || string(rootAfter) != string(public) {
		t.Fatal("restart replaced trusted CA")
	}
}

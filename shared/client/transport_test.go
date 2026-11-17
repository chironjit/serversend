package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func dualTransportFixture(t *testing.T, handler http.Handler, quicEnabled bool) (*httptest.Server, *http3.Server) {
	t.Helper()
	tcp := httptest.NewUnstartedServer(handler)
	tcp.EnableHTTP2 = true
	tcp.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	tcp.StartTLS()
	t.Cleanup(tcp.Close)
	address := strings.TrimPrefix(tcp.URL, "https://")
	udp, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	if !quicEnabled {
		// An open, silent UDP socket models a firewall that drops packets.
		return tcp, nil
	}
	server := &http3.Server{Handler: handler, TLSConfig: tcp.TLS.Clone(), QUICConfig: &quic.Config{Allow0RTT: false}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(udp) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	return tcp, server
}

func testPasswordResponse(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(api.PasswordAttempt{NextStep: "second_factor", Challenge: "challenge"})
}

func TestHTTP3VerifiedSelectionAndNoEarlyCredentials(t *testing.T) {
	var probes, passwords atomic.Int64
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 3 || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version != tls.VersionTLS13 {
			t.Error("request did not use fully authenticated HTTP/3 TLS")
		}
		if r.Method == "HEAD" {
			probes.Add(1)
			if r.Header.Get("Authorization") != "" || r.ContentLength > 0 || r.URL.Path != api.BasePath+"/capabilities" {
				t.Error("probe carried credentials or targeted a mutation")
			}
			return
		}
		passwords.Add(1)
		testPasswordResponse(w)
	}), true)
	c, err := New(server.URL, fingerprint(server.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	for range 2 {
		if _, err := c.Password(t.Context(), "user", "secret"); err != nil {
			t.Fatal(err)
		}
	}
	if probes.Load() != 1 || passwords.Load() != 2 {
		t.Fatalf("probes=%d passwords=%d", probes.Load(), passwords.Load())
	}
}

func TestHTTP3BlockedUDPFallsBackOnceWithConcurrentRequests(t *testing.T) {
	var received atomic.Int64
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.TLS.Version != tls.VersionTLS13 {
			t.Error("fallback weakened TLS or HTTP/2")
		}
		received.Add(1)
		testPasswordResponse(w)
	}), false)
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	transport := c.http.Transport.(*adaptiveTransport)
	dial := transport.h3.Dial
	var attempts atomic.Int64
	transport.h3.Dial = func(ctx context.Context, address string, config *tls.Config, qconfig *quic.Config) (*quic.Conn, error) {
		attempts.Add(1)
		return dial(ctx, address, config, qconfig)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if _, err := c.Password(ctx, "user", "secret"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("fallback took %s", elapsed)
	}
	if attempts.Load() != 1 || received.Load() != 8 {
		t.Fatalf("dial attempts=%d requests=%d", attempts.Load(), received.Load())
	}
}

func TestHTTP3WrongPinAndRedirectNeverReleaseCredentials(t *testing.T) {
	var received atomic.Int64
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		testPasswordResponse(w)
	}), true)
	c, err := NewWithOptions(server.URL, strings.Repeat("0", 64), TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if _, err := c.Password(t.Context(), "user", "secret"); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("wrong pin: %v", err)
	}
	if received.Load() != 0 {
		t.Fatal("unapproved peer received request bytes")
	}

	var redirected atomic.Int64
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	redirect, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			return
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}), true)
	trusted, err := NewWithOptions(redirect.URL, fingerprint(redirect.Certificate()), TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(trusted.Close)
	if _, err := trusted.Password(t.Context(), "user", "secret"); err == nil {
		t.Fatal("redirect accepted as a valid response")
	}
	if redirected.Load() != 0 {
		t.Fatal("credentials reached redirect destination")
	}
}

func TestHTTP3UncertainMutationIsNotReplayed(t *testing.T) {
	var h3Hits, h2Hits atomic.Int64
	var quicServer *http3.Server
	server, q := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			return
		}
		if r.ProtoMajor == 3 {
			h3Hits.Add(1)
			go quicServer.Close()
			<-r.Context().Done()
			return
		}
		h2Hits.Add(1)
		testPasswordResponse(w)
	}), true)
	quicServer = q
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := c.Password(ctx, "user", "secret"); err == nil {
		t.Fatal("lost response unexpectedly succeeded")
	}
	if h3Hits.Load() != 1 || h2Hits.Load() != 0 {
		t.Fatalf("mutation replayed: h3=%d h2=%d", h3Hits.Load(), h2Hits.Load())
	}
	if _, err := c.Password(ctx, "user", "secret"); err != nil {
		t.Fatal(err)
	}
	if h2Hits.Load() != 1 {
		t.Fatal("next request did not use HTTP/2 fallback")
	}
}

func TestHTTP3ResponseHeadersHaveBoundedWait(t *testing.T) {
	var h2Hits atomic.Int64
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return
		}
		if r.ProtoMajor == 2 {
			h2Hits.Add(1)
			testPasswordResponse(w)
			return
		}
		<-r.Context().Done()
	}), true)
	c, err := NewWithOptions(server.URL, fingerprint(server.Certificate()), TransportOptions{PreferHTTP3: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.http.Transport.(*adaptiveTransport).headerTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := c.Password(ctx, "user", "secret"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("header timeout: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("response header wait was not bounded")
	}
	if h2Hits.Load() != 0 {
		t.Fatal("header timeout replayed a mutation")
	}
	if _, err := c.Password(ctx, "user", "secret"); err != nil {
		t.Fatal(err)
	}
	if h2Hits.Load() != 1 {
		t.Fatal("header timeout did not select HTTP/2 fallback")
	}
}

func TestTransportSelectionRepeatsOnlyWhenConnectionCloses(t *testing.T) {
	for _, quicEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP2", true: "HTTP3"}[quicEnabled], func(t *testing.T) {
			var protocol atomic.Int64
			server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				protocol.Store(int64(r.ProtoMajor))
				testPasswordResponse(w)
			}), quicEnabled)
			c, err := New(server.URL, fingerprint(server.Certificate()))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			transport := c.http.Transport.(*adaptiveTransport)
			dial := transport.h3.Dial
			var attempts atomic.Int64
			transport.h3.Dial = func(ctx context.Context, address string, config *tls.Config, qconfig *quic.Config) (*quic.Conn, error) {
				attempts.Add(1)
				return dial(ctx, address, config, qconfig)
			}
			request := func() {
				t.Helper()
				if _, err := c.Password(t.Context(), "user", "secret"); err != nil {
					t.Fatal(err)
				}
			}
			request()
			request()
			if attempts.Load() != 1 {
				t.Fatalf("stable connection retried QUIC: %d", attempts.Load())
			}
			transport.mu.Lock()
			closeConnection(transport.quicConn)
			closeConnection(transport.tcpConn)
			transport.mu.Unlock()
			request()
			want := int64(2)
			if quicEnabled {
				want = 3
			}
			if attempts.Load() != 2 || protocol.Load() != want {
				t.Fatalf("reconnect: QUIC attempts=%d protocol=%d", attempts.Load(), protocol.Load())
			}
		})
	}
}

func TestHTTP2ConnectionDoesNotUpgradeWhenQUICBecomesAvailable(t *testing.T) {
	var expected atomic.Int64
	expected.Store(2)
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int64(r.ProtoMajor) != expected.Load() {
			t.Error("connection used the wrong transport")
		}
		testPasswordResponse(w)
	}), true)
	c, err := New(server.URL, fingerprint(server.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	transport := c.http.Transport.(*adaptiveTransport)
	dial := transport.h3.Dial
	transport.h3.Dial = func(context.Context, string, *tls.Config, *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("UDP unavailable")
	}
	if _, err := c.Password(t.Context(), "user", "secret"); err != nil {
		t.Fatal(err)
	}
	transport.h3.Dial = dial
	for range 3 {
		if _, err := c.Password(t.Context(), "user", "secret"); err != nil {
			t.Fatal(err)
		}
	}
	// Once the fallback socket actually closes, the now-available QUIC path
	// must win on the very next request, without constructing a new client.
	transport.mu.Lock()
	_ = transport.tcpConn.Close()
	transport.mu.Unlock()
	expected.Store(3)
	if _, err := c.Password(t.Context(), "user", "secret"); err != nil {
		t.Fatal(err)
	}

}

func TestCertificateProbeUsesQUICWithoutTCP(t *testing.T) {
	server, _ := dualTransportFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("certificate inspection sent application data")
	}), true)
	server.Close()
	probe, err := Probe(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Fingerprint != fingerprint(server.Certificate()) {
		t.Fatal("wrong QUIC certificate")
	}
}

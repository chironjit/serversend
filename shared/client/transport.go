package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

// TransportOptions controls transport selection for one approved origin.
// Alternate hosts and ports are never learned from untrusted Alt-Svc headers.
type TransportOptions struct {
	PreferHTTP3 bool
}

const quicProbeTimeout = 500 * time.Millisecond

type adaptiveTransport struct {
	origin        string
	h2            *http.Transport
	h3            *http3.Transport
	mu            sync.Mutex
	probe         chan struct{}
	quicConn      *http3.ClientConn
	tcpConn       *http2.ClientConn
	fallback      bool
	closed        bool
	headerTimeout time.Duration
}

func newAdaptiveTransport(origin string, h2 *http.Transport) *adaptiveTransport {
	return &adaptiveTransport{origin: origin, h2: h2, headerTimeout: h2.ResponseHeaderTimeout, h3: &http3.Transport{
		TLSClientConfig:        h2.TLSClientConfig.Clone(),
		MaxResponseHeaderBytes: 16 << 10,
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout:       quicProbeTimeout,
			MaxIdleTimeout:             time.Minute,
			MaxStreamReceiveWindow:     8 << 20,
			MaxConnectionReceiveWindow: 16 << 20,
		},
		// DialAddr waits for the handshake. Never use DialAddrEarly or the
		// special GET_0RTT / HEAD_0RTT methods, even for the public probe.
		Dial: func(ctx context.Context, address string, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
			return quic.DialAddr(ctx, address, tlsConfig, config)
		},
	}}
}

// connection serializes establishment, but all requests share the selected
// multiplexed connection. There is no upgrade timer: each replacement connection
// starts with QUIC, followed by HTTP/2 only if that QUIC attempt fails.
func (t *adaptiveTransport) connection(ctx context.Context) (http.RoundTripper, error) {
	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return nil, errors.New("client is closed")
		}
		if t.quicConn != nil && t.quicConn.Context().Err() == nil {
			conn := t.quicConn
			t.mu.Unlock()
			return conn, nil
		}
		if t.tcpConn != nil {
			state := t.tcpConn.State()
			if !state.Closed && !state.Closing {
				conn := t.tcpConn
				t.mu.Unlock()
				return conn, nil
			}
		}
		if pending := t.probe; pending != nil {
			t.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		t.probe = make(chan struct{})
		fallback := t.fallback
		t.fallback = false
		t.mu.Unlock()
		conn, err := t.connect(ctx, fallback)
		t.mu.Lock()
		if t.closed {
			closeConnection(conn)
			err = errors.New("client is closed")
		} else if err == nil {
			t.quicConn, _ = conn.(*http3.ClientConn)
			t.tcpConn, _ = conn.(*http2.ClientConn)
		}
		close(t.probe)
		t.probe = nil
		t.mu.Unlock()
		return conn, err
	}
}

func (t *adaptiveTransport) connect(ctx context.Context, fallback bool) (http.RoundTripper, error) {
	u, _ := url.Parse(t.origin)
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "443")
	}
	if !fallback {
		probeCtx, cancel := context.WithTimeout(ctx, quicProbeTimeout)
		config := t.h3.TLSClientConfig.Clone()
		config.ServerName, config.NextProtos = u.Hostname(), []string{http3.NextProtoH3}
		q, err := t.h3.Dial(probeCtx, address, config, t.h3.QUICConfig)
		var conn *http3.ClientConn
		if err == nil {
			conn = t.h3.NewClientConn(q)
			request, _ := http.NewRequestWithContext(probeCtx, http.MethodHead, t.origin+api.BasePath+"/capabilities", nil)
			response, probeErr := conn.RoundTrip(request)
			err = probeErr
			if response != nil {
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = errors.New("HTTP/3 capability probe rejected")
				}
			}
			if err != nil {
				closeConnection(conn)
			}
		}
		cancel()
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, ErrIdentityChanged) {
			return nil, ErrIdentityChanged
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := t.h2.TLSClientConfig.Clone()
	config.ServerName, config.NextProtos = u.Hostname(), []string{http2.NextProtoTLS}
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: t.h2.TLSHandshakeTimeout}, Config: config}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if conn.(*tls.Conn).ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		conn.Close()
		return nil, errors.New("server did not negotiate HTTP/2 fallback")
	}
	transport := &http2.Transport{IdleConnTimeout: t.h2.IdleConnTimeout, MaxHeaderListSize: 16 << 10, StrictMaxConcurrentStreams: true}
	client, err := transport.NewClientConn(conn)
	if err != nil {
		conn.Close()
	}
	return client, err
}

func closeConnection(conn http.RoundTripper) {
	switch conn := conn.(type) {
	case *http3.ClientConn:
		if conn != nil {
			_ = conn.CloseWithError(0, "connection closed")
		}
	case *http2.ClientConn:
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func (t *adaptiveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != t.origin {
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, errors.New("request destination differs from the approved server origin")
	}
	conn, err := t.connection(r.Context())
	if err != nil {
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, err
	}
	response, err := t.roundTrip(conn, r)
	if err == nil || r.Context().Err() != nil {
		return response, err
	}
	t.mu.Lock()
	// Only change the connection that actually failed; another caller may
	// already have established its replacement.
	if conn == t.quicConn {
		t.quicConn = nil
		t.fallback = true
		closeConnection(conn)
	} else if conn == t.tcpConn {
		t.tcpConn = nil
		closeConnection(conn)
	}
	t.mu.Unlock()
	// A failed mutation may already have committed. Return its uncertainty
	// instead of silently replaying it. Subsequent requests retain the same
	// Client/session/upload IDs. A failed QUIC connection selects HTTP/2 next.
	if errors.Is(err, ErrIdentityChanged) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return response, err
	}
	if r.Body != nil && r.Body != http.NoBody {
		return response, err
	}
	conn, err = t.connection(r.Context())
	if err != nil {
		return nil, err
	}
	return t.roundTrip(conn, r.Clone(r.Context()))
}

type cancelResponseBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (b *cancelResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// Bound response-header waits on both protocols without imposing the same
// deadline on a response body that is making progress.
func (t *adaptiveTransport) roundTrip(conn http.RoundTripper, r *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(r.Context())
	timer := time.AfterFunc(t.headerTimeout, func() { cancel(context.DeadlineExceeded) })
	response, err := conn.RoundTrip(r.Clone(ctx))
	timer.Stop()
	if err != nil {
		if _, quic := conn.(*http3.ClientConn); quic && r.Body != nil {
			r.Body.Close()
		}
		if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			err = fmt.Errorf("response header timeout: %w", context.DeadlineExceeded)
		}
		cancel(nil)
		return response, err
	}
	response.Body = &cancelResponseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func (t *adaptiveTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	closeConnection(t.quicConn)
	closeConnection(t.tcpConn)
	return t.h3.Close()
}

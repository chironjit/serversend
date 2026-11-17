package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// ServeTransports serves the same authenticated API and storage over TLS/TCP
// and QUIC/UDP. QUIC failure leaves HTTP/2 available for client fallback.
// TCP failure or cancellation stops both listeners.
// Call Close only after this returns, as with Serve.
func (s *Server) ServeTransports(ctx context.Context, tcp net.Listener, udp net.PacketConn) error {
	defer tcp.Close()
	defer udp.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	handler := httpapi.New(s.ready, httpapi.WithIdentity(s.signIdentity), httpapi.WithAuthentication(s.accounts), httpapi.WithTransfers(s.transfers), httpapi.WithHTTP3(), httpapi.WithLogs(s.logs))
	return s.serveTransports(ctx, cancel, tcp, udp, handler)
}

func (s *Server) serveTransports(ctx context.Context, cancel context.CancelFunc, tcp net.Listener, udp net.PacketConn, handler http.Handler) error {
	h3 := &http3.Server{
		Handler: requestDeadlines(handler, 30*time.Second), TLSConfig: s.tls.Clone(),
		QUICConfig: &quic.Config{
			Allow0RTT:                  false,
			HandshakeIdleTimeout:       5 * time.Second,
			MaxIdleTimeout:             time.Minute,
			MaxIncomingStreams:         32,
			MaxStreamReceiveWindow:     8 << 20,
			MaxConnectionReceiveWindow: 16 << 20,
		},
		MaxHeaderBytes: 16 << 10, IdleTimeout: time.Minute,
	}
	defer h3.Close()
	transport := &quic.Transport{Conn: udp, ConnContext: func(ctx context.Context, _ *quic.ClientInfo) (context.Context, error) {
		return httpapi.IdentityConnectionContext(ctx), nil
	}}
	// ServeListener returns when accepting stops, before active responses
	// finish. The shared UDP transport must outlive the complete HTTP/3 drain.
	defer transport.Close()
	tcpDone, quicDone := make(chan error, 1), make(chan error, 1)
	go func() { tcpDone <- s.serve(ctx, tcp, handler) }()
	go func() {
		listener, err := transport.ListenEarly(http3.ConfigureTLSConfig(h3.TLSConfig), h3.QUICConfig)
		if err == nil {
			// Stop new handshakes without aborting accepted connections.
			defer listener.Close()
			err = h3.ServeListener(listener)
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		quicDone <- err
	}()
	var first error
	tcpStopped, quicStopped := false, false
	for !tcpStopped {
		select {
		case <-ctx.Done():
			goto drain
		case first = <-tcpDone:
			tcpStopped = true
		case err := <-quicDone:
			quicStopped = true
			quicDone = nil
			_ = udp.Close()
			if err != nil {
				s.logs.Add("warn", fmt.Sprintf("HTTP/3 unavailable; continuing with HTTP/2 fallback: %v", err))
			}
		}
	}
drain:
	s.draining.Store(true)
	cancel()
	drainCtx, drainCancel := context.WithTimeout(context.Background(), s.drainTimeout)
	defer drainCancel()
	drainErr := h3.Shutdown(drainCtx)
	if drainErr != nil {
		_ = h3.Close()
	}
	if !tcpStopped {
		first = errors.Join(first, <-tcpDone)
	}
	if !quicStopped {
		first = errors.Join(first, <-quicDone)
	}
	if err := errors.Join(first, drainErr); err != nil {
		return fmt.Errorf("serve HTTPS transports: %w", err)
	}
	return nil
}

func requestDeadlines(handler http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(timeout))
		_ = controller.SetWriteDeadline(time.Now().Add(timeout))
		handler.ServeHTTP(w, r)
	})
}

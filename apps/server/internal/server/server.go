// Package server composes the server process without hiding startup failures.
package server

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/apps/server/internal/identity"
)

const shutdownTimeout = 10 * time.Second

type Server struct {
	db           *sql.DB
	tls          *tls.Config
	identity     identity.Identity
	draining     atomic.Bool
	drainTimeout time.Duration
}

// New validates TLS and writable deployment paths before creating metadata. It
// does not open a listener. Close must be called after serving has stopped.
func New(ctx context.Context, c config.Config) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.CheckStorage(); err != nil {
		return nil, err
	}
	tlsConfig, err := loadTLS(c, time.Now())
	if err != nil {
		return nil, err
	}
	db, err := database.Open(ctx, c.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("open metadata: %w", err)
	}
	id, err := identity.LoadOrCreate(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Server{db: db, tls: tlsConfig, identity: id, drainTimeout: shutdownTimeout}, nil
}

// Identity returns public material only; it is not itself proof of trust.
func (s *Server) Identity() identity.Identity { return s.identity }

func (s *Server) Close() error { return s.db.Close() }

// Serve owns listener and serves TLS only. Cancellation marks the process
// unready before draining requests. A timeout forcibly closes active requests.
// The instance is intended for a single Serve call.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	return s.serve(ctx, listener, httpapi.New(s.ready))
}

func (s *Server) ready(ctx context.Context) error {
	if s.draining.Load() {
		return errors.New("server is draining")
	}
	return s.db.PingContext(ctx)
}

func (s *Server) serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	httpServer := &http.Server{
		Handler: handler, TLSConfig: s.tls.Clone(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	defer httpServer.Close()
	// ServeTLS takes listener ownership even when the context is already done.
	if err := ctx.Err(); err != nil {
		listener.Close()
		return err
	}
	stopped := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			s.draining.Store(true)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), s.drainTimeout)
			defer cancel()
			shutdownDone <- httpServer.Shutdown(shutdownCtx)
		case <-stopped:
			shutdownDone <- nil
		}
	}()
	err := httpServer.ServeTLS(listener, "", "")
	close(stopped)
	shutdownErr := <-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTPS: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("drain HTTPS requests: %w", shutdownErr)
	}
	return nil
}

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

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/auth/twofactor"
	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/apps/server/internal/identity"
	"github.com/chironjit/serversend/shared/protocol"
)

const shutdownTimeout = 10 * time.Second

type Server struct {
	db           *sql.DB
	tls          *tls.Config
	identity     identity.Identity
	accounts     *auth.Service
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
	accounts, err := newAccounts(ctx, db, c.ConfigDir, id)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Server{db: db, tls: tlsConfig, identity: id, accounts: accounts, drainTimeout: shutdownTimeout}, nil
}

// newAccounts loads the TOTP protection key before any account request can be
// served. A key is created only while no encrypted secret depends on one; a
// missing key beside retained secrets is a restore fault that stops startup.
func newAccounts(ctx context.Context, db *sql.DB, configDir string, id identity.Identity) (*auth.Service, error) {
	repo := database.NewRepository(db)
	retained, err := repo.HasTOTPSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect two-factor state: %w", err)
	}
	key, err := twofactor.LoadProtectionKey(configDir, !retained)
	if err != nil {
		return nil, fmt.Errorf("load TOTP protection key (restore totp.key with the database if accounts are enrolled): %w", err)
	}
	protector, err := twofactor.NewSecretProtector(key)
	if err != nil {
		return nil, err
	}
	return auth.New(repo, auth.NewArgon2id(), nil, auth.WithTwoFactor(protocol.ID(id.ID), protector))
}

// Accounts exposes account operations to local operator commands.
func (s *Server) Accounts() *auth.Service { return s.accounts }

// Identity returns public material only; it is not itself proof of trust.
func (s *Server) Identity() identity.Identity { return s.identity }

func (s *Server) Close() error { return s.db.Close() }

// Serve owns listener and serves TLS only. Cancellation marks the process
// unready before draining requests. A timeout forcibly closes active requests.
// The instance is intended for a single Serve call.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	return s.serve(ctx, listener, httpapi.New(s.ready, httpapi.WithAuthentication(s.accounts)))
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

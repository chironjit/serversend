package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("serversend", flag.ContinueOnError)
	check := flags.Bool("check", false, "validate TLS, storage, and metadata without serving")
	bootstrap := flags.String("bootstrap-admin", "", "create the first administrator `username` and print its one-hour temporary password, without serving")
	recoverBootstrap := flags.String("recover-bootstrap-admin", "", "reissue the temporary password for the sole not-yet-enrolled administrator `username`, without serving")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments; use serversend -help")
	}
	if *bootstrap != "" && *recoverBootstrap != "" || *check && *bootstrap+*recoverBootstrap != "" {
		return errors.New("use only one of -check, -bootstrap-admin and -recover-bootstrap-admin")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	s, err := server.New(ctx, c)
	if err != nil {
		return err
	}
	defer s.Close()
	if c.TLSIPs != "" {
		log.Printf("Private IPv4 HTTPS enabled; export the public trust certificate from %s through a trusted operator channel", filepath.Join(c.ConfigDir, "tls", "ca.crt"))
	}
	if *bootstrap != "" || *recoverBootstrap != "" {
		return issueAdministrator(ctx, s.Accounts(), *bootstrap, *recoverBootstrap, out)
	}
	if *check {
		log.Printf("Configuration and metadata ready; server ID %s", s.Identity().ID)
		return nil
	}
	listener, err := net.Listen("tcp", c.TCPAddress)
	if err != nil {
		return fmt.Errorf("listen TCP: %w", err)
	}
	log.Printf("Server %s listening for HTTPS on %s", s.Identity().ID, listener.Addr())
	log.Printf("Certificate fingerprint for desktop approval: %s", s.CertificateFingerprint())
	return s.Serve(ctx, listener)
}

// issueAdministrator writes the temporary password only to the operator's
// standard output, never to the log. Network clients cannot reach this path.
func issueAdministrator(ctx context.Context, accounts *auth.Service, bootstrap, recovery string, out io.Writer) error {
	var issued auth.IssuedPassword
	var err error
	if bootstrap != "" {
		issued, err = accounts.BootstrapAdmin(ctx, bootstrap)
	} else {
		issued, err = accounts.RecoverBootstrapAdmin(ctx, recovery)
	}
	switch {
	case errors.Is(err, database.ErrBootstrapUsed) && bootstrap != "":
		return errors.New("administrator bootstrap is closed: an account already exists; if the first administrator never finished onboarding, use -recover-bootstrap-admin")
	case errors.Is(err, database.ErrBootstrapUsed):
		return errors.New("bootstrap recovery applies only to the sole administrator before TOTP enrollment; other accounts are reset by an administrator")
	case errors.Is(err, auth.ErrInput):
		return errors.New("username must be 1 to 128 characters of a-z, 0-9, '.', '_' or '-'")
	case err != nil:
		return fmt.Errorf("issue administrator credential: %w", err)
	}
	_, err = fmt.Fprintf(out, "Administrator: %s\nTemporary password: %s\nExpires: %s\nSign in, replace the password and enroll TOTP before it expires. This password is not shown again.\n",
		issued.Username, issued.TemporaryPassword, issued.ExpiresAt.Local().Format("2006-01-02 15:04:05 MST"))
	return err
}

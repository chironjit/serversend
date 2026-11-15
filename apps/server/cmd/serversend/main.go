package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serversend", flag.ContinueOnError)
	check := flags.Bool("check", false, "validate TLS, storage, and metadata without serving")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments; use serversend -help")
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
	if *check {
		log.Printf("Configuration and metadata ready; server ID %s", s.Identity().ID)
		return nil
	}
	listener, err := net.Listen("tcp", c.TCPAddress)
	if err != nil {
		return fmt.Errorf("listen TCP: %w", err)
	}
	log.Printf("Server %s listening for HTTPS on %s", s.Identity().ID, listener.Addr())
	return s.Serve(ctx, listener)
}

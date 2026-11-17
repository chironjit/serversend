package client

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"time"

	"github.com/chironjit/serversend/shared/api"
)

// retryTransfer is only for operations whose caller preserves their immutable
// request identity. Authentication and arbitrary mutations must never use it.
// Six attempts allow short outages without occupying workers indefinitely.
func retryTransfer(ctx context.Context, operation func() error) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = operation()
		if err == nil || !transientTransferError(err) || attempt == 5 {
			return err
		}
		delay := 250 * time.Millisecond << attempt
		if jitter, e := rand.Int(rand.Reader, big.NewInt(int64(delay/2))); e == nil {
			delay += time.Duration(jitter.Int64())
		}
		var failure api.Error
		if errors.As(err, &failure) && failure.RetryAfterSeconds > 0 {
			// Do not retry earlier than the server permits, or occupy a worker for
			// an unbounded server-supplied duration.
			if failure.RetryAfterSeconds > 30 {
				return err
			}
			delay = max(delay, time.Duration(failure.RetryAfterSeconds)*time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func transientTransferError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrIdentityChanged) {
		return false
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return false
	}
	var failure api.Error
	if errors.As(err, &failure) {
		return failure.Retryable()
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var transport *url.Error
	return errors.As(err, &transport) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded)
}

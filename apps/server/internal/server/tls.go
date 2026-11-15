package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
)

func loadTLS(c config.Config, now time.Time) (*tls.Config, error) {
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return nil, errors.New("TLS is required: set SERVER_SEND_TLS_CERT_FILE and SERVER_SEND_TLS_KEY_FILE")
	}
	certificate, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate and key: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse TLS certificate: %w", err)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("TLS certificate is expired or not yet valid; renew it before starting")
	}
	if len(leaf.ExtKeyUsage)+len(leaf.UnknownExtKeyUsage) > 0 {
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			serverAuth = serverAuth || usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny
		}
		if !serverAuth {
			return nil, errors.New("TLS certificate does not permit server authentication")
		}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

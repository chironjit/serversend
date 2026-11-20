package server

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
)

// loadTLS also returns the public private-CA certificate (PEM), or nil when
// externally supplied certificates are used.
func loadTLS(c config.Config, now time.Time) (*tls.Config, []byte, error) {
	if c.TLSIPs != "" {
		addresses, err := c.PrivateTLSAddresses()
		if err != nil {
			return nil, nil, err
		}
		manager, err := localtls.Open(c.ConfigDir, addresses)
		if err != nil {
			return nil, nil, err
		}
		return manager.TLSConfig(), manager.PublicCA(), nil
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return nil, nil, errors.New("TLS is required: set SERVER_SEND_TLS_IPS for private IPv4 HTTPS, or SERVER_SEND_TLS_CERT_FILE and SERVER_SEND_TLS_KEY_FILE")
	}
	certificate, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load TLS certificate and key: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("parse TLS certificate: %w", err)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, nil, errors.New("TLS certificate is expired or not yet valid; renew it before starting")
	}
	if len(leaf.ExtKeyUsage)+len(leaf.UnknownExtKeyUsage) > 0 {
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			serverAuth = serverAuth || usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny
		}
		if !serverAuth {
			return nil, nil, errors.New("TLS certificate does not permit server authentication")
		}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
	}, nil, nil
}

// CertificateFingerprint is the SHA-256 of the certificate currently served,
// in the form the desktop shows before a user approves a connection. It is
// public information. It changes whenever the certificate is renewed.
func (s *Server) CertificateFingerprint() string {
	var certificate *tls.Certificate
	if len(s.tls.Certificates) > 0 {
		certificate = &s.tls.Certificates[0]
	} else if s.tls.GetCertificate != nil {
		certificate, _ = s.tls.GetCertificate(&tls.ClientHelloInfo{})
	}
	if certificate == nil || len(certificate.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(certificate.Certificate[0])
	digest := hex.EncodeToString(sum[:])
	groups := make([]string, 0, 16)
	for i := 0; i < len(digest); i += 4 {
		groups = append(groups, digest[i:i+4])
	}
	return strings.Join(groups, " ")
}

// CAFingerprint is the OpenSSL-style SHA-256 fingerprint of the private CA, or
// empty when external certificates are used.
func (s *Server) CAFingerprint() string { return localtls.Fingerprint(s.ca) }

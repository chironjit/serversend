package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// Trust contains public material only. An empty AuthorityPEM uses OS roots.
// Private anchors must be supplied by the operator through a trusted channel.
type Trust struct {
	AuthorityPEM string                   `json:"authority_pem,omitempty"`
	Identity     protocol.TrustedIdentity `json:"identity"`
}

type identityVerifier struct {
	mu    sync.Mutex
	trust Trust
}

func AuthorityFingerprint(authority string) (string, error) {
	if len(authority) > 64<<10 {
		return "", errors.New("certificate exceeds limit")
	}
	block, rest := pem.Decode([]byte(authority))
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || !bytes.HasPrefix(bytes.TrimSpace([]byte(authority)), []byte("-----BEGIN CERTIFICATE-----")) || len(bytes.TrimSpace(rest)) != 0 {
		return "", errors.New("choose one public CA certificate in PEM format")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return "", errors.New("choose the server's public CA certificate")
	}
	return fingerprint(cert), nil
}

// NewVerified gates every newly established multiplexed connection with a
// TLS-channel-bound identity proof before sending any application credential.
// A zero identity is paired using verified TLS and must be saved before login.
func NewVerified(origin string, trust Trust) (*Client, error) {
	if err := protocol.ValidateOrigin(origin); err != nil {
		return nil, err
	}
	var roots *x509.CertPool
	if trust.AuthorityPEM != "" {
		if _, err := AuthorityFingerprint(trust.AuthorityPEM); err != nil {
			return nil, err
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(trust.AuthorityPEM)) {
			return nil, errors.New("invalid private CA")
		}
	}
	if trust.Identity.ServerID != "" || trust.Identity.PublicKey != "" {
		if err := trust.Identity.Validate(); err != nil {
			return nil, err
		}
	}
	verifier := &identityVerifier{trust: trust}
	h2 := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 60 * time.Second}
	transport := newAdaptiveTransport(origin, h2)
	transport.authenticate = verifier.verify
	return &Client{origin: origin, identity: verifier, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) VerifiedTrust() (Trust, error) {
	if c.identity == nil {
		return Trust{}, errors.New("repair server trust before saving credentials")
	}
	c.identity.mu.Lock()
	defer c.identity.mu.Unlock()
	if c.identity.trust.Identity.Validate() != nil {
		return Trust{}, errors.New("verify the server identity first")
	}
	return c.identity.trust, nil
}
func (c *Client) VerifyIdentity(ctx context.Context) error {
	return c.exchange(ctx, call{method: "GET", path: "/capabilities"}, nil, nil)
}
func (v *identityVerifier) verify(ctx context.Context, origin string, conn http.RoundTripper, state tls.ConnectionState) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return ErrIdentityChanged
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	challenge := protocol.IdentityChallenge{Nonce: base64.RawURLEncoding.EncodeToString(nonce), Origin: origin}
	body, _ := json.Marshal(challenge)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", origin+api.BasePath+"/identity/proof", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := conn.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("server identity verification failed; repair the connection before signing in")
	}
	proof, err := api.DecodeIdentityProof(io.LimitReader(response.Body, api.MaxEventBytes+1))
	if err != nil {
		return ErrIdentityChanged
	}
	trusted := v.trust.Identity
	if trusted.ServerID == "" {
		trusted = proof.TrustedIdentity
	}
	exporter, err := state.ExportKeyingMaterial(protocol.IdentityExporterLabel, nil, 32)
	if err != nil {
		return err
	}
	if err = proof.Verify(challenge, trusted, state.PeerCertificates[0].Raw, exporter, time.Now()); err != nil {
		return ErrIdentityChanged
	}
	v.trust.Identity = trusted
	return nil
}

func (c *Client) SetInstallationID(id protocol.ID) { c.installationID = id }

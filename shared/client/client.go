// Package client is the desktop-side HTTPS client for one server. It never
// follows redirects, so credentials cannot be carried to another origin, and
// it keeps the access token inside the Go process.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

const maxJSONResponse = 1 << 20

// ErrIdentityChanged means certificate pinning or the signed identity proof
// failed. No application credential was released to that connection.
var ErrIdentityChanged = errors.New("the server identity does not match the approved trust; repair the connection before signing in")

// ServerProbe describes what a server presented before anything is trusted.
// PubliclyTrusted reports that the operating system already trusts the
// certificate; otherwise the user must compare Fingerprint with the server
// operator's value before approving it.
type ServerProbe struct {
	AuthorityFingerprint string    `json:"authority_fingerprint,omitempty"`
	Origin               string    `json:"origin"`
	Fingerprint          string    `json:"fingerprint"`
	Subject              string    `json:"subject"`
	ExpiresAt            time.Time `json:"expires_at"`
	PubliclyTrusted      bool      `json:"publicly_trusted"`
}

func fingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// Probe tries QUIC first, then TLS/TCP, sends no application data, and reports the presented
// leaf certificate. It establishes no trust by itself.
func Probe(ctx context.Context, origin string) (ServerProbe, error) {
	if err := protocol.ValidateOrigin(origin); err != nil {
		return ServerProbe{}, fmt.Errorf("enter the server address as https://host:port: %w", err)
	}
	parsed, _ := url.Parse(origin)
	name, port := parsed.Hostname(), parsed.Port()
	if port == "" {
		port = "443"
	}
	host := net.JoinHostPort(name, port)
	var probe ServerProbe
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: name, InsecureSkipVerify: true, // Inspection only; nothing is sent.
		VerifyConnection: func(state tls.ConnectionState) error {
			leaf := state.PeerCertificates[0]
			probe = ServerProbe{Origin: origin, Fingerprint: fingerprint(leaf), Subject: leaf.Subject.String(), ExpiresAt: leaf.NotAfter}
			intermediates := x509.NewCertPool()
			for _, certificate := range state.PeerCertificates[1:] {
				intermediates.AddCert(certificate)
			}
			_, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Intermediates: intermediates})
			probe.PubliclyTrusted = err == nil
			return nil
		},
	}}
	quicConfig := dialer.Config.Clone()
	quicConfig.NextProtos = []string{http3.NextProtoH3}
	quicCtx, cancel := context.WithTimeout(ctx, quicProbeTimeout)
	quicConn, quicErr := quic.DialAddr(quicCtx, host, quicConfig, &quic.Config{HandshakeIdleTimeout: quicProbeTimeout})
	cancel()
	if quicErr == nil {
		_ = quicConn.CloseWithError(0, "certificate inspection complete")
		return probe, nil
	}
	if err := ctx.Err(); err != nil {
		return ServerProbe{}, err
	}
	connection, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return ServerProbe{}, fmt.Errorf("could not reach a ServerSend server at %s: %w", origin, err)
	}
	connection.Close()
	return probe, nil
}

type Client struct {
	identity       *identityVerifier
	installationID protocol.ID
	appVersion     string
	origin         string
	http           *http.Client

	mu                      sync.Mutex
	session                 api.Session
	refreshMu               sync.Mutex
	pending                 *api.RefreshRequest
	refreshChallenge        string
	refreshChallengeExpires time.Time
	store                   func(CredentialState) error
	limits                  protocol.TransferLimits
}

// New is the legacy leaf-pin client used by transport fixtures and benchmarks.
// Production desktop connections must use NewVerified; this client cannot
// supply verified trust for credential persistence.
func New(origin, approvedFingerprint string) (*Client, error) {
	return NewWithOptions(origin, approvedFingerprint, TransportOptions{PreferHTTP3: true})
}

// NewWithOptions permits HTTP/2-only measurements. Production callers use New,
// which tries HTTP/3 first on every connection establishment.
func NewWithOptions(origin, approvedFingerprint string, options TransportOptions) (*Client, error) {
	if err := protocol.ValidateOrigin(origin); err != nil {
		return nil, err
	}
	if len(approvedFingerprint) != 64 {
		return nil, errors.New("an approved certificate fingerprint is required")
	}
	transport := &http.Transport{
		ForceAttemptHTTP2: true, ResponseHeaderTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 60 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // Replaced by the exact pin below.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 || fingerprint(state.PeerCertificates[0]) != approvedFingerprint {
					return ErrIdentityChanged
				}
				return nil
			}},
	}
	var selected http.RoundTripper = transport
	if options.PreferHTTP3 {
		selected = newAdaptiveTransport(origin, transport)
	}
	return &Client{origin: origin, http: &http.Client{Transport: selected, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Close() {
	c.http.CloseIdleConnections()
	if closer, ok := c.http.Transport.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (c *Client) Origin() string { return c.origin }

// Account returns the signed-in identity without the access token.
func (c *Client) Account() (api.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	public := c.session
	public.AccessToken = ""
	public.RefreshCredential = ""
	return public, c.session.AccessToken != ""
}

func (c *Client) token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.AccessToken
}

type call struct {
	method, path string
	token        string
	header       http.Header
	body         []byte
	json         bool
}

// do returns the response for a 2xx status and a typed api.Error otherwise.
func (c *Client) do(ctx context.Context, request call) (*http.Response, error) {
	if request.token != "" && c.token() != "" && !strings.HasPrefix(request.path, "/auth/") && request.path != "/sessions" {
		if err := c.EnsureSession(ctx); err != nil {
			return nil, err
		}
		request.token = c.token()
	}

	r, err := http.NewRequestWithContext(ctx, request.method, c.origin+api.BasePath+request.path, bytes.NewReader(request.body))
	if err != nil {
		return nil, err
	}
	for name, values := range request.header {
		r.Header[name] = values
	}
	if request.json {
		r.Header.Set("Content-Type", "application/json")
	}
	if request.token != "" {
		r.Header.Set("Authorization", "Bearer "+request.token)
	}
	response, err := c.http.Do(r)
	if err != nil {
		if errors.Is(err, ErrIdentityChanged) {
			return nil, ErrIdentityChanged
		}
		return nil, fmt.Errorf("could not reach the server: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	if failure, err := api.DecodeError(response.Body); err == nil {
		return nil, failure
	}
	return nil, fmt.Errorf("the server answered with unexpected status %d", response.StatusCode)
}

func (c *Client) exchange(ctx context.Context, request call, input, output any) error {
	if input != nil {
		body, err := json.Marshal(input)
		if err != nil {
			return err
		}
		request.body, request.json = body, true
	}
	response, err := c.do(ctx, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if output == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil || len(body) > maxJSONResponse {
		return errors.New("the server sent an unreadable or oversized response")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return errors.New("the server sent a response this version cannot read")
	}
	return nil
}

// Password starts sign-in. The returned challenge authorizes only NextStep.
func (c *Client) Password(ctx context.Context, username, password string) (api.PasswordAttempt, error) {
	var attempt api.PasswordAttempt
	err := c.exchange(ctx, call{method: "POST", path: "/auth/password"}, api.PasswordAuthentication{Username: username, Password: password}, &attempt)
	return attempt, err
}

func (c *Client) ReplacePassword(ctx context.Context, challenge, password string) error {
	return c.exchange(ctx, call{method: "POST", path: "/auth/password/replace", token: challenge}, api.PasswordReplacement{Password: password}, nil)
}

func (c *Client) EnrollTOTP(ctx context.Context, challenge string) (api.TOTPEnrollment, error) {
	var enrollment api.TOTPEnrollment
	err := c.exchange(ctx, call{method: "POST", path: "/auth/totp/enroll", token: challenge}, struct{}{}, &enrollment)
	return enrollment, err
}

func (c *Client) ConfirmTOTP(ctx context.Context, challenge, code string) ([]string, error) {
	var codes api.RecoveryCodes
	err := c.exchange(ctx, call{method: "POST", path: "/auth/totp/confirm", token: challenge}, api.SecondFactorCode{Code: code}, &codes)
	return codes.Codes, err
}

// SignIn completes the second factor and keeps the resulting session in
// memory only; it is gone when the process exits.
func (c *Client) SignIn(ctx context.Context, challenge, code, deviceName string) (api.Session, error) {
	var session api.Session
	if err := c.exchange(ctx, call{method: "POST", path: "/sessions", token: challenge}, api.SessionRequest{InstallationID: c.installationID, Code: code, DeviceName: deviceName, Platform: runtime.GOOS, AppVersion: c.appVersion}, &session); err != nil {
		return api.Session{}, err
	}
	if session.AccessToken == "" || (protocol.Scope{ServerID: session.ServerID, AccountID: session.AccountID}).Validate() != nil {
		return api.Session{}, errors.New("the server sent an incomplete session")
	}
	c.refreshMu.Lock()
	c.pending = nil
	c.refreshChallenge = ""
	c.refreshChallengeExpires = time.Time{}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	c.refreshMu.Unlock()
	account, _ := c.Account()
	return account, nil
}

// SignOut always forgets the local session, even if the server is unreachable.
func (c *Client) SignOut(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	session := c.session
	c.session = api.Session{}
	c.mu.Unlock()
	c.pending = nil
	c.refreshChallenge = ""
	c.refreshChallengeExpires = time.Time{}
	if session.RefreshCredential != "" {
		return c.exchange(ctx, call{method: "POST", path: "/sessions/revoke"}, api.RefreshRequest{DeviceID: session.DeviceID, RefreshCredential: session.RefreshCredential}, nil)
	}
	if session.AccessToken == "" {
		return nil
	}
	return c.exchange(ctx, call{method: "DELETE", path: "/sessions/current", token: session.AccessToken}, nil, nil)
}

func (c *Client) transferLimits(ctx context.Context) (protocol.TransferLimits, error) {
	c.mu.Lock()
	limits := c.limits
	c.mu.Unlock()
	if limits.Validate() == nil {
		return limits, nil
	}
	response, err := c.do(ctx, call{method: "GET", path: "/capabilities"})
	if err != nil {
		return limits, err
	}
	defer response.Body.Close()
	capabilities, err := api.DecodeCapabilities(response.Body)
	if err != nil {
		return limits, errors.New("the server's capabilities are not supported by this version")
	}
	c.mu.Lock()
	c.limits = capabilities.Limits
	c.mu.Unlock()
	return capabilities.Limits, nil
}

// Catalog lists the signed-in account's files.
func (c *Client) Catalog(ctx context.Context) (api.CatalogPage, error) {
	return c.CatalogFolder(ctx, "")
}

func (c *Client) CatalogFolder(ctx context.Context, parent protocol.ID) (api.CatalogPage, error) {
	if parent != "" && parent.Validate() != nil {
		return api.CatalogPage{}, errors.New("invalid folder ID")
	}
	response, err := c.do(ctx, call{method: "GET", path: "/catalog?parent_id=" + string(parent), token: c.token()})
	if err != nil {
		return api.CatalogPage{}, err
	}
	defer response.Body.Close()
	page, err := api.DecodeCatalogPage(response.Body, nil)
	if err != nil {
		return api.CatalogPage{}, errors.New("the server sent a file list this version cannot read")
	}
	return page, nil
}

// SavedSession returns credentials for the native OS credential store only.
func (c *Client) SavedSession() api.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// RestoreSession validates the stored token against live server state over pinned TLS.
func (c *Client) RestoreSession(ctx context.Context, session api.Session) error {
	var current api.Session
	if session.AccessToken == "" {
		return errors.New("sign in again")
	}
	if err := c.exchange(ctx, call{method: "GET", path: "/sessions/current", token: session.AccessToken}, nil, &current); err != nil {
		return err
	}
	if current.AccountID != session.AccountID || current.ServerID != session.ServerID || current.Role != "user" {
		return errors.New("saved account does not match server")
	}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	return nil
}

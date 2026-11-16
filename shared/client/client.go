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
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

const maxJSONResponse = 1 << 20

// ErrIdentityChanged means the server presented a different certificate from
// the one the user approved. No request, and so no credential, was sent.
var ErrIdentityChanged = errors.New("the server's certificate does not match the one approved for this connection")

// ServerProbe describes what a server presented before anything is trusted.
// PubliclyTrusted reports that the operating system already trusts the
// certificate; otherwise the user must compare Fingerprint with the server
// operator's value before approving it.
type ServerProbe struct {
	Origin          string    `json:"origin"`
	Fingerprint     string    `json:"fingerprint"`
	Subject         string    `json:"subject"`
	ExpiresAt       time.Time `json:"expires_at"`
	PubliclyTrusted bool      `json:"publicly_trusted"`
}

func fingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// Probe opens one TLS connection, sends nothing, and reports the presented
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
	connection, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return ServerProbe{}, fmt.Errorf("could not reach a ServerSend server at %s: %w", origin, err)
	}
	connection.Close()
	return probe, nil
}

type Client struct {
	origin string
	http   *http.Client

	mu      sync.Mutex
	session api.Session
	limits  protocol.TransferLimits
}

// New returns a client that accepts exactly the approved certificate. Every
// connection is checked against the pin before any request bytes are written.
func New(origin, approvedFingerprint string) (*Client, error) {
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
	return &Client{origin: origin, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Origin() string { return c.origin }

// Account returns the signed-in identity without the access token.
func (c *Client) Account() (api.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	public := c.session
	public.AccessToken = ""
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
	if err := c.exchange(ctx, call{method: "POST", path: "/sessions", token: challenge}, api.SessionRequest{Code: code, DeviceName: deviceName}, &session); err != nil {
		return api.Session{}, err
	}
	if session.AccessToken == "" || (protocol.Scope{ServerID: session.ServerID, AccountID: session.AccountID}).Validate() != nil {
		return api.Session{}, errors.New("the server sent an incomplete session")
	}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	account, _ := c.Account()
	return account, nil
}

// SignOut always forgets the local session, even if the server is unreachable.
func (c *Client) SignOut(ctx context.Context) error {
	token := c.token()
	c.mu.Lock()
	c.session = api.Session{}
	c.mu.Unlock()
	if token == "" {
		return nil
	}
	return c.exchange(ctx, call{method: "DELETE", path: "/sessions/current", token: token}, nil, nil)
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
	response, err := c.do(ctx, call{method: "GET", path: "/catalog", token: c.token()})
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

package client

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

// routeTransport selects only operator-approved endpoints. Connection probes
// carry no credentials; each endpoint independently verifies TLS and identity.
// Application requests are never replayed here after an ambiguous failure.
type routeTransport struct {
	mu       sync.Mutex
	primary  string
	routes   []*adaptiveTransport
	failed   []time.Time
	selected int
}

func NewVerifiedRoutes(origin string, endpoints []string, trust Trust) (*Client, error) {
	if len(endpoints) > 7 {
		return nil, errors.New("at most eight routes are supported")
	}
	if len(endpoints) > 0 && trust.Identity.Validate() != nil {
		return nil, errors.New("pair the server before approving alternate routes")
	}
	c, err := NewVerified(origin, trust)
	if err != nil {
		return nil, err
	}
	t := &routeTransport{primary: origin, routes: []*adaptiveTransport{c.http.Transport.(*adaptiveTransport)}, failed: make([]time.Time, 1)}
	seen := map[string]bool{origin: true}
	for _, endpoint := range endpoints {
		if protocol.ValidateOrigin(endpoint) != nil || seen[endpoint] {
			t.Close()
			return nil, errors.New("routes must be distinct HTTPS origins")
		}
		seen[endpoint] = true
		route, e := NewVerified(endpoint, trust)
		if e != nil {
			t.Close()
			return nil, e
		}
		t.routes = append(t.routes, route.http.Transport.(*adaptiveTransport))
		t.failed = append(t.failed, time.Time{})
	}
	c.http.Transport = t
	return c, nil
}

func (t *routeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != t.primary {
		return nil, errors.New("unapproved request origin")
	}
	t.mu.Lock()
	start := t.selected
	t.mu.Unlock()
	var last error
	for offset := range t.routes {
		i := (start + offset) % len(t.routes)
		t.mu.Lock()
		blocked := time.Now().Before(t.failed[i])
		t.mu.Unlock()
		if blocked {
			continue
		}
		route := t.routes[i]
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		_, err := route.connection(ctx)
		cancel()
		if err != nil {
			var certificate *tls.CertificateVerificationError
			if errors.Is(err, ErrIdentityChanged) || errors.As(err, &certificate) {
				return nil, errors.Join(ErrIdentityChanged, err)
			}
			if r.Context().Err() != nil {
				return nil, r.Context().Err()
			}
			t.mu.Lock()
			t.failed[i] = time.Now().Add(10 * time.Second)
			t.mu.Unlock()
			last = err
			continue
		}
		t.mu.Lock()
		t.selected = i
		t.mu.Unlock()
		request := r.Clone(r.Context())
		target, _ := url.Parse(route.origin)
		request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
		request.Host = target.Host
		response, err := route.RoundTrip(request)
		if err != nil && r.Context().Err() == nil {
			t.mu.Lock()
			t.failed[i] = time.Now().Add(10 * time.Second)
			t.mu.Unlock()
		}
		return response, err
	}
	if last == nil {
		last = errors.New("approved routes are temporarily unavailable")
	}
	return nil, last
}
func (t *routeTransport) CloseIdleConnections() {
	for _, r := range t.routes {
		r.h2.CloseIdleConnections()
	}
}
func (t *routeTransport) Close() error {
	for _, r := range t.routes {
		_ = r.Close()
	}
	return nil
}

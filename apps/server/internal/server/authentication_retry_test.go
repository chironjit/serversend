package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
)

// Test onboarding shares the real limiter. Retry only an explicit rejection
// before authentication mutates state, never an uncertain transport failure.
func retryAuthentication[T any](ctx context.Context, call func() (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, err
		}
		result, err := call()
		var limited api.Error
		if attempt >= 2 || !errors.As(err, &limited) || limited.Code != api.Overloaded {
			return result, err
		}
		timer := time.NewTimer(time.Duration(min(5, max(1, limited.RetryAfterSeconds))) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

func TestAuthenticationRetryDoesNotReplayOtherFailures(t *testing.T) {
	for _, failure := range []error{api.Error{Code: api.AuthenticationRequired}, errors.New("connection lost after request")} {
		attempts := 0
		_, err := retryAuthentication(t.Context(), func() (struct{}, error) { attempts++; return struct{}{}, failure })
		if err != failure || attempts != 1 {
			t.Fatal("non-rate-limit error retried", err, attempts)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	_, err := retryAuthentication(ctx, func() (struct{}, error) {
		attempts++
		cancel()
		return struct{}{}, api.Error{Code: api.Overloaded, RetryAfterSeconds: 5}
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatal("cancellation did not stop retries", err, attempts)
	}
}

func TestAuthenticationRetryIsBounded(t *testing.T) {
	attempts := 0
	_, err := retryAuthentication(t.Context(), func() (struct{}, error) {
		attempts++
		return struct{}{}, api.Error{Code: api.Overloaded, RetryAfterSeconds: 1}
	})
	var limited api.Error
	if attempts != 3 || !errors.As(err, &limited) || limited.Code != api.Overloaded {
		t.Fatal("persistent rejection not returned", attempts, err)
	}
}

// Force the CI failure at the actual HTTP boundary rather than relying on
// machine-dependent Argon2 timings to exhaust the limiter.
func TestOnboardingRetriesRateLimitedSessionWithoutRepeatingEnrollment(t *testing.T) {
	s, err := New(t.Context(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	handler := httpapi.New(s.ready, httpapi.WithAuthentication(s.accounts))
	var mu sync.Mutex
	calls := map[string]int{}
	var rejectedAt time.Time
	origin := startDualServer(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		reject := r.URL.Path == api.BasePath+"/sessions" && calls[r.URL.Path] == 1
		if reject {
			rejectedAt = time.Now()
		}
		if r.URL.Path == api.BasePath+"/sessions" && !reject && time.Since(rejectedAt) < time.Second {
			t.Error("retried before Retry-After")
		}
		mu.Unlock()
		if reject {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(api.ErrorResponse{Error: api.Error{Code: api.Overloaded, RetryAfterSeconds: 1, Message: "Authentication busy"}})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	issued, err := s.accounts.BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	onboardedTransportClient(t, origin, strings.ReplaceAll(s.CertificateFingerprint(), " ", ""), "admin", issued.TemporaryPassword, client.TransportOptions{PreferHTTP3: true})
	mu.Lock()
	defer mu.Unlock()
	for path, want := range map[string]int{"/auth/password": 3, "/auth/password/replace": 1, "/auth/totp/enroll": 1, "/auth/totp/confirm": 1, "/sessions": 2} {
		if got := calls[api.BasePath+path]; got != want {
			t.Errorf("%s requests = %d, want %d", path, got, want)
		}
	}
	var devices int
	if err = s.db.QueryRow(`SELECT count(*) FROM device_sessions`).Scan(&devices); err != nil || devices != 1 {
		t.Fatal("session retry duplicated device authorization", devices, err)
	}
}

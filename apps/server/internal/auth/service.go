// Package auth owns account authentication and restricted onboarding attempts.
// An onboarding challenge is never a device or browser administrator session.
package auth

import (
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/protocol"
	"sync"
	"time"
)

var (
	ErrCredentials    = errors.New("credentials are invalid or expired")
	ErrInput          = errors.New("invalid account input")
	ErrPasswordPolicy = errors.New("use a password with 12 to 128 characters and at most 256 bytes, without control characters")
	ErrForbidden      = errors.New("reauthenticated administrator access is required")
	ErrOverloaded     = errors.New("too many authentication attempts; try again shortly")
)

const TemporaryPasswordLifetime = time.Hour
const ChallengeLifetime = 10 * time.Minute

// PasswordHasher isolates the expensive, bounded password implementation so
// deterministic state-machine tests do not require production KDF resources.
type PasswordHasher interface {
	Hash(string) (string, error)
	Verify(encoded, password string) bool
}

type Service struct {
	repo       *database.Repository
	passwords  PasswordHasher
	now        func() time.Time
	dummyHash  string
	workers    chan struct{}
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// Principal is produced only after full authentication by the session layer.
// Its private fields prevent handlers from constructing it from caller JSON.
// Each admin operation rechecks account role, onboarding and revocation epoch.
type Principal struct {
	accountID         protocol.ID
	epoch             int64
	reauthenticatedAt time.Time
}

func New(repo *database.Repository, passwords PasswordHasher, now func() time.Time) (*Service, error) {
	if repo == nil || passwords == nil {
		return nil, errors.New("authentication repository and password hasher are required")
	}
	if now == nil {
		now = time.Now
	}
	dummy, err := passwords.Hash("ServerSend unknown account timing placeholder")
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, passwords: passwords, now: now, dummyHash: dummy, workers: make(chan struct{}, 2), tokens: 10, lastRefill: now()}, nil
}

func (s *Service) acquire() (func(), error) {
	s.mu.Lock()
	now := s.now()
	s.tokens = min(10, s.tokens+max(0, now.Sub(s.lastRefill).Seconds())*2)
	s.lastRefill = now
	if s.tokens < 1 {
		s.mu.Unlock()
		return nil, ErrOverloaded
	}
	s.tokens--
	s.mu.Unlock()
	select {
	case s.workers <- struct{}{}:
		return func() { <-s.workers }, nil
	default:
		return nil, ErrOverloaded
	}
}

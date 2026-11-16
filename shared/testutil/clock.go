// Package testutil provides deterministic test adapters. These sources must never
// be used for production secrets, timekeeping, or filesystem durability.
package testutil

import (
	"sync"
	"time"
)

// Clock changes only when explicitly advanced; it does not run timers.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(now time.Time) *Clock      { return &Clock{now: now} }
func (c *Clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

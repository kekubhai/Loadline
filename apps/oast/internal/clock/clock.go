// Package clock supplies the time source used by the registry and rate
// limiter. Tests inject Manual so expiration and refill behavior can be
// exercised without sleeping.
package clock

import (
	"sync"
	"time"
)

// Clock is the minimal time source the OAST engine needs.
type Clock interface {
	Now() time.Time
}

// System is the production clock. It always returns UTC.
type System struct{}

// Now implements Clock.
func (System) Now() time.Time { return time.Now().UTC() }

// Func adapts a function to Clock.
type Func func() time.Time

// Now implements Clock.
func (f Func) Now() time.Time { return f().UTC() }

// Manual is a concurrency-safe clock for deterministic tests.
type Manual struct {
	mu  sync.Mutex
	now time.Time
}

// NewManual returns a manual clock starting at start.
func NewManual(start time.Time) *Manual {
	return &Manual{now: start.UTC()}
}

// Now implements Clock.
func (m *Manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// Advance moves the clock forward by d.
func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}

// Set moves the clock to t.
func (m *Manual) Set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = t.UTC()
}

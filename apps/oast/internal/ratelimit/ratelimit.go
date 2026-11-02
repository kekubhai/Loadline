// Package ratelimit implements a small concurrency-safe token bucket.
//
// The bucket is intentionally global per listener: it caps the total work
// an unauthenticated callback surface can force, without keeping per-source
// state that an attacker could grow without bound.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
)

// Limiter is a token bucket driven by an injected Clock.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	clock  clock.Clock
}

// New creates a limiter that refills at ratePerSecond up to burst tokens.
func New(ratePerSecond float64, burst int, clk clock.Clock) *Limiter {
	if clk == nil {
		clk = clock.System{}
	}
	capacity := float64(burst)
	return &Limiter{
		rate:   ratePerSecond,
		burst:  capacity,
		tokens: capacity,
		last:   clk.Now(),
		clock:  clk,
	}
}

// Allow consumes one token and reports whether the request may proceed.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	elapsed := now.Sub(l.last).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	l.tokens = math.Min(l.burst, l.tokens+elapsed*l.rate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

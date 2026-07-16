// Package gateway holds gatewayd's edge logic: per-app rate limiting, the host→
// app routing cache, and the wake gate that holds requests during a cold wake
// (spec §4.1). The HTTP/TLS server wires these together; each piece here is a
// self-contained, testable unit.
package gateway

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Limiter is a per-app token-bucket rate limiter (spec §4.1). Each app refills at
// its plan's rps with a plan burst; an over-limit request is rejected (the caller
// returns 429). The clock is injectable so the refill math is tested precisely.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	rps    float64
	burst  float64
	last   time.Time
}

// NewLimiter returns a limiter using the wall clock.
func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, now: time.Now}
}

// Allow reports whether a request for appID on plan may proceed, consuming a
// token if so. Plan rps/burst come from the limits table (never inlined).
func (l *Limiter) Allow(appID string, plan api.Plan) bool {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[appID]
	if b == nil {
		b = &bucket{tokens: float64(limits.RateLimitBurst), rps: float64(limits.RateLimitRPS), burst: float64(limits.RateLimitBurst), last: now}
		l.buckets[appID] = b
	} else {
		// A plan change updates the bucket's parameters without losing tokens.
		b.rps, b.burst = float64(limits.RateLimitRPS), float64(limits.RateLimitBurst)
		b.tokens += now.Sub(b.last).Seconds() * b.rps
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Forget drops an app's bucket (e.g. on delete) to bound memory.
func (l *Limiter) Forget(appID string) {
	l.mu.Lock()
	delete(l.buckets, appID)
	l.mu.Unlock()
}

// ForgetAll drops every bucket and returns the count dropped. SIGHUP and the
// apid-side "purge all" callback use this so an operator can recover memory
// after a mass-delete without bouncing the daemon.
func (l *Limiter) ForgetAll() int {
	l.mu.Lock()
	n := len(l.buckets)
	l.buckets = map[string]*bucket{}
	l.mu.Unlock()
	return n
}

// BucketCount returns the number of buckets currently held. /metrics and the
// SIGHUP observability log use this.
func (l *Limiter) BucketCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

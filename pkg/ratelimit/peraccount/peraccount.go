// Package peraccount is a per-account token-bucket pool for
// rate-limiting telemetry writes (ADR-127 PR-B and PR-D).
//
// History: extracted from cmd/apid/grpc_server_request_telemetry.go
// in ADR-127 PR-D so both the apid IncrementRequestTelemetry gRPC
// receiver (PR-B) and the gatewayd-public OTel spans writer
// (PR-D) share the same per-account bucket semantics.
//
// Behavior: refill rate is `bucketCap / 60` tokens per second;
// bucket capacity is the full `bucketCap` (one minute's worth of
// tokens, so a customer can burst one full minute before
// back-pressure kicks in). A bucketCap of 0 disables the limiter —
// every Take returns rate-limited, matching the "plan doesn't
// include telemetry" code path.
//
// Concurrency: the inner maps are guarded by a single mutex. The
// per-bucket ops are O(1) (refill calc + token decrement). For
// thousands of accounts the contention is fine — the receivers'
// hot path runs once per row, not per goroutine.
//
// Plan caching: the limiter caches the resolved api.Limits per
// account so a sustained-overflow customer doesn't pay an
// AccountByID round-trip per row. The cache TTL is 60s — a plan
// upgrade takes effect within a minute (well under the customer's
// perception).
package peraccount

import (
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// Limiter is the per-account token-bucket pool. The zero value is
// not usable; call NewLimiter. The receiver methods are nil-safe
// for the metrics hook (SetMetricsObserver) but the Take / Cache
// / Cached paths require a real Limiter.
type Limiter struct {
	mu      sync.Mutex
	bucket  map[uuid.UUID]*accountBucket
	limits  map[uuid.UUID]api.Limits // plan-derived caps, TTL 60s
	cacheAt map[uuid.UUID]time.Time
	now     func() time.Time // clock injection seam for tests
}

// accountBucket retains its capacity across caller-supplied cap drift.
// Only CacheLimits, which receives an authoritative account lookup, may change
// an existing capacity. This permits plan changes without allowing fallback
// callers to oscillate the same bucket's rate.
type accountBucket struct {
	tokens     float64
	lastRefill time.Time
	cap        int
}

// NewLimiter wires an empty limiter using time.Now() as the clock.
// Tests can swap the clock via SetClock before calling Take.
func NewLimiter() *Limiter {
	return &Limiter{
		bucket:  make(map[uuid.UUID]*accountBucket),
		limits:  make(map[uuid.UUID]api.Limits),
		cacheAt: make(map[uuid.UUID]time.Time),
		now:     time.Now,
	}
}

// SetClock swaps the clock used by Take for cache-refill
// calculations. Pass a function returning a deterministic
// monotonic time in tests. Restoring production behavior is a
// SetClock(time.Now) call.
func (r *Limiter) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// Take returns (true, 0) when a token was taken successfully, or
// (false, retryAfterMs) when the bucket was empty. bucketCap
// (DebugTelemetryRequestsPerMinute) caps the bucket; a value of
// 0 disables the limiter (every call is rate-limited — the
// customer's plan doesn't include telemetry).
func (r *Limiter) Take(accountID uuid.UUID, bucketCap int) (bool, int64) {
	if bucketCap <= 0 {
		return false, 60_000 // 60s — match the standard rate-limit hint
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b, ok := r.bucket[accountID]
	if !ok {
		if limits, resolved := r.limits[accountID]; resolved {
			bucketCap = limits.DebugTelemetryRequestsPerMinute
		}
		if bucketCap <= 0 {
			return false, 60_000
		}
		b = &accountBucket{tokens: float64(bucketCap), lastRefill: now, cap: bucketCap}
		r.bucket[accountID] = b
	} else if _, resolved := r.limits[accountID]; !resolved && b.cap != bucketCap {
		slog.Default().Warn("peraccount: caller cap differs from bucket; awaiting resolved account limits",
			"account_id", accountID, "bucket_cap", b.cap, "caller_cap", bucketCap)
	}
	b.refill(now)
	if b.cap <= 0 {
		return false, 60_000
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Round up the remaining fractional-token delay: truncating can tell a
	// caller to retry before any token is actually available.
	retryMs := int64(math.Ceil((1 - b.tokens) * 60_000 / float64(b.cap)))
	return false, max(1, retryMs)
}

func (b *accountBucket) refill(now time.Time) {
	if elapsed := now.Sub(b.lastRefill).Seconds(); elapsed > 0 {
		b.tokens = min(float64(b.cap), b.tokens+elapsed*float64(b.cap)/60)
		b.lastRefill = now
	}
}

// CacheLimits stores authoritative account caps and updates an existing bucket.
// Settle elapsed time at the old rate first, then clamp its balance to the new
// capacity. Refreshes neither grant fresh bursts nor retroactively refill at a
// newly upgraded rate. Called per AccountByID round-trip, not per row.
func (r *Limiter) CacheLimits(accountID uuid.UUID, limits api.Limits) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if b := r.bucket[accountID]; b != nil {
		b.refill(now)
		b.cap = max(0, limits.DebugTelemetryRequestsPerMinute)
		b.tokens = min(b.tokens, float64(b.cap))
	}
	r.limits[accountID] = limits
	r.cacheAt[accountID] = now
}

// CachedLimits returns the cached limits + true if fresh, or
// zero limits + false if the cache entry is older than 60s or
// absent. Caller is responsible for the AccountByID round-trip
// on cache miss.
func (r *Limiter) CachedLimits(accountID uuid.UUID) (api.Limits, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cachedAt, ok := r.cacheAt[accountID]
	if !ok || r.now().Sub(cachedAt) > 60*time.Second {
		return api.Limits{}, false
	}
	return r.limits[accountID], true
}

package gateway

import (
	"sync"
	"time"
)

const (
	defaultRetryBudgetWindow = 10 * time.Second
	maxRetryBudgetScopes     = 10_000
)

// RetryBudget caps aggregate replay amplification per app over a short fixed
// window. Attempt limits protect one request; this budget protects the fleet
// when many requests fail at once.
type RetryBudget struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	buckets map[string]retryBudgetBucket
}

type retryBudgetBucket struct {
	started   time.Time
	originals int
	retries   int
}

// NewRetryBudget creates an app-scoped aggregate retry budget. A nil clock
// and non-positive window select production defaults.
func NewRetryBudget(window time.Duration, now func() time.Time) *RetryBudget {
	if window <= 0 {
		window = defaultRetryBudgetWindow
	}
	if now == nil {
		now = time.Now
	}
	return &RetryBudget{window: window, now: now, buckets: make(map[string]retryBudgetBucket)}
}

// ObserveOriginal records one request admitted under a retry policy.
func (b *RetryBudget) ObserveOriginal(scope string) {
	if b == nil || scope == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	bucket, ok := b.bucketLocked(scope, now)
	if !ok {
		return
	}
	bucket.originals++
	b.buckets[scope] = bucket
}

// AllowRetry atomically spends one retry token. The allowance is the larger
// of minRetries and ceil(originals*percent/100). Unknown scopes are denied:
// callers must observe the original first.
func (b *RetryBudget) AllowRetry(scope string, percent, minRetries int) bool {
	if b == nil {
		return true
	}
	if scope == "" {
		return false
	}
	if percent < 0 {
		percent = 0
	}
	if minRetries < 0 {
		minRetries = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	bucket, ok := b.buckets[scope]
	if !ok || now.Sub(bucket.started) >= b.window {
		return false
	}
	allowance := (bucket.originals*percent + 99) / 100
	if allowance < minRetries {
		allowance = minRetries
	}
	if bucket.retries >= allowance {
		return false
	}
	bucket.retries++
	b.buckets[scope] = bucket
	return true
}

func (b *RetryBudget) bucketLocked(scope string, now time.Time) (retryBudgetBucket, bool) {
	if bucket, ok := b.buckets[scope]; ok {
		if now.Sub(bucket.started) < b.window {
			return bucket, true
		}
		bucket = retryBudgetBucket{started: now}
		b.buckets[scope] = bucket
		return bucket, true
	}
	if len(b.buckets) >= maxRetryBudgetScopes {
		for key, bucket := range b.buckets {
			if now.Sub(bucket.started) >= b.window {
				delete(b.buckets, key)
			}
		}
	}
	if len(b.buckets) >= maxRetryBudgetScopes {
		return retryBudgetBucket{}, false
	}
	bucket := retryBudgetBucket{started: now}
	b.buckets[scope] = bucket
	return bucket, true
}

type retryBudgetAdmission struct {
	budget *RetryBudget
	scope  string
}

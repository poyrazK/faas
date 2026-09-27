package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
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
	remote  redis.UniversalClient
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
	if b.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = retryBudgetObserveScript.Run(ctx, b.remote, []string{retryBudgetRemoteKey(scope)}, b.window.Milliseconds()).Err()
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
	if b.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		admitted, err := retryBudgetAdmitScript.Run(ctx, b.remote, []string{retryBudgetRemoteKey(scope)}, percent, minRetries).Int()
		// A shared-backend failure must never silently restore a per-process
		// allowance; declining a replay preserves the fleet safety bound.
		return err == nil && admitted == 1
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

const retryBudgetRemotePrefix = "gregale:retry-budget:v1:"

var retryBudgetObserveScript = redis.NewScript(`
local count = redis.call('HINCRBY', KEYS[1], 'originals', 1)
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count
`)

var retryBudgetAdmitScript = redis.NewScript(`
local originals = tonumber(redis.call('HGET', KEYS[1], 'originals') or '0')
if originals == 0 then return 0 end
local allowance = math.floor((originals * tonumber(ARGV[1]) + 99) / 100)
if allowance < tonumber(ARGV[2]) then allowance = tonumber(ARGV[2]) end
local spent = tonumber(redis.call('HGET', KEYS[1], 'retries') or '0')
if spent >= allowance then return 0 end
redis.call('HINCRBY', KEYS[1], 'retries', 1)
return 1
`)

func retryBudgetRemoteKey(scope string) string { return retryBudgetRemotePrefix + scope }

// NewRedisRetryBudget makes one Redis hash authoritative for an app's retries
// across gateway processes. The returned budget fails closed on Redis errors.
func NewRedisRetryBudget(parent context.Context, rawURL string, window time.Duration) (*RetryBudget, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid retry-budget Redis URL")
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 100 * time.Millisecond
	opts.WriteTimeout = 100 * time.Millisecond
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	budget := NewRetryBudget(window, nil)
	budget.remote = client
	return budget, nil
}

func (b *RetryBudget) Close() error {
	if b == nil || b.remote == nil {
		return nil
	}
	return b.remote.Close()
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

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRetryBudgetWindow = api.TrafficRetryBudgetWindow
	maxRetryBudgetScopes     = 10_000
)

// RetryBudget caps aggregate replay amplification per app over a short fixed
// window. Attempt limits protect one request; this budget protects the fleet
// when many requests fail at once.
type RetryBudget struct {
	mu        sync.Mutex
	window    time.Duration
	now       func() time.Time
	buckets   map[string]retryBudgetBucket
	remote    redis.UniversalClient
	shared    SharedRetryBudgetBackend
	backendID string
	observer  retryBudgetObserver
}

// SharedRetryBudgetBackend owns atomic observations and retry spends across
// processes. The backend's clock owns expiry; gateway clocks cannot reset it.
type SharedRetryBudgetBackend interface {
	ObserveOriginal(context.Context, string, time.Duration) error
	AllowRetry(context.Context, string, int, int) (bool, error)
	BackendID() string
}

func NewSharedRetryBudget(backend SharedRetryBudgetBackend) (*RetryBudget, error) {
	if backend == nil {
		return nil, errors.New("shared retry budget requires a backend")
	}
	b := NewRetryBudget(0, nil)
	b.shared, b.backendID = backend, backend.BackendID()
	return b, nil
}

type retryBudgetObserver interface {
	RecordRetryBudgetOperation(operation, result string)
}

// WithObserver records bounded backend outcomes. Configure it before the
// budget is used; the gateway installs it during startup.
func (b *RetryBudget) WithObserver(observer retryBudgetObserver) *RetryBudget {
	if b != nil {
		b.observer = observer
	}
	return b
}

func (b *RetryBudget) recordOperation(operation, result string) {
	if b.observer != nil {
		b.observer.RecordRetryBudgetOperation(operation, result)
	}
}

type retryBudgetBucket struct {
	started   time.Time
	originals int
	retries   int
}

// NewRetryBudget creates an explicitly process-local app retry budget. A nil
// clock and non-positive window select the default clock and window duration.
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
func (b *RetryBudget) ObserveOriginal(parent context.Context, scope string) bool {
	if b == nil || scope == "" {
		return b == nil
	}
	if b.shared != nil {
		ctx, cancel := context.WithTimeout(parent, api.TrafficCounterOperationTimeout)
		defer cancel()
		if err := b.shared.ObserveOriginal(ctx, scope, b.window); err != nil {
			b.recordOperation("observe", "error")
			return false
		}
		b.recordOperation("observe", "ok")
		return true
	}
	if b.remote != nil {
		ctx, cancel := context.WithTimeout(parent, api.TrafficCounterOperationTimeout)
		defer cancel()
		if err := retryBudgetObserveScript.Run(ctx, b.remote, []string{retryBudgetRemoteKey(scope)}, b.window.Milliseconds()).Err(); err != nil {
			b.recordOperation("observe", "error")
			return false
		} else {
			b.recordOperation("observe", "ok")
		}
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	bucket, ok := b.bucketLocked(scope, now)
	if !ok {
		return false
	}
	bucket.originals++
	b.buckets[scope] = bucket
	return true
}

// AllowRetry atomically spends one retry token. The allowance is the larger
// of minRetries and ceil(originals*percent/100). Unknown scopes are denied:
// callers must observe the original first.
func (b *RetryBudget) AllowRetry(parent context.Context, scope string, percent, minRetries int) bool {
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
	if b.shared != nil {
		ctx, cancel := context.WithTimeout(parent, api.TrafficCounterOperationTimeout)
		defer cancel()
		allowed, err := b.shared.AllowRetry(ctx, scope, percent, minRetries)
		if err != nil {
			b.recordOperation("admit", "error")
			return false
		}
		result := "denied"
		if allowed {
			result = "allowed"
		}
		b.recordOperation("admit", result)
		return allowed
	}
	if b.remote != nil {
		ctx, cancel := context.WithTimeout(parent, api.TrafficCounterOperationTimeout)
		defer cancel()
		admitted, err := retryBudgetAdmitScript.Run(ctx, b.remote, []string{retryBudgetRemoteKey(scope)}, percent, minRetries).Int()
		// A shared-backend failure must never silently restore a per-process
		// allowance; declining a replay preserves the fleet safety bound.
		if err != nil {
			b.recordOperation("admit", "error")
			return false
		}
		if admitted == 1 {
			b.recordOperation("admit", "allowed")
			return true
		}
		b.recordOperation("admit", "denied")
		return false
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
	// Hash the transport endpoint and logical DB, never the URL credentials.
	endpoint := fmt.Sprintf("%s|%s|%d|%t", opts.Network, opts.Addr, opts.DB, opts.TLSConfig != nil)
	sum := sha256.Sum256([]byte(endpoint))
	budget.backendID = hex.EncodeToString(sum[:8])
	return budget, nil
}

// BackendID is a credential-free, stable identity for the shared endpoint.
// A process-local budget returns an empty identity.
func (b *RetryBudget) BackendID() string {
	if b == nil {
		return ""
	}
	return b.backendID
}

// CounterMode reports the backend wired into this budget. It does not prove
// backend availability or successful fleet accounting for any request.
func (b *RetryBudget) CounterMode() string {
	if b == nil {
		return "unwired"
	}
	if b.shared != nil {
		return "shared"
	}
	if b.remote != nil {
		return "redis"
	}
	return "local"
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

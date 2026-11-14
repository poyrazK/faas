package events

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
)

// publishLimiterAccounts bounds the per-account buckets one process keeps;
// the map is cleared when full, which only refills buckets early.
const publishLimiterAccounts = 10000

// PublishLimiter applies the per-account event publish rate
// (api.EventPublishLimits, ADR-20261026140000) in one process. Every
// publication path counts each event once, so a burst of publishes cannot
// queue on the account's publication lock and hold database connections.
type PublishLimiter struct {
	// Now is the limiter's clock; nil is time.Now.
	Now func() time.Time

	mu      sync.Mutex
	buckets map[string]*publishBucket
}

type publishBucket struct {
	limiter *rate.Limiter
	limits  api.EventPublishLimits
}

// Allow takes n events from the account's bucket. When the bucket cannot
// cover them now it takes nothing and reports how long until it can. A nil
// limiter or a zero rate admits everything.
func (l *PublishLimiter) Allow(accountID string, limits api.EventPublishLimits, n int) (time.Duration, bool) {
	if l == nil || limits.PerSecond <= 0 || n <= 0 {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil || len(l.buckets) >= publishLimiterAccounts {
		l.buckets = map[string]*publishBucket{}
	}
	bucket := l.buckets[accountID]
	if bucket == nil || bucket.limits != limits {
		bucket = &publishBucket{limiter: rate.NewLimiter(rate.Limit(limits.PerSecond), max(limits.Burst, 1)), limits: limits}
		l.buckets[accountID] = bucket
	}
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	reservation := bucket.limiter.ReserveN(now, n)
	if !reservation.OK() {
		// More events than the bucket ever holds: wait for a full bucket.
		return time.Duration(float64(time.Second) * float64(max(limits.Burst, 1)) / float64(limits.PerSecond)), false
	}
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

// SharedPublishRate is the store's shared per-account publish counter
// (state.PgStore). Every apid replica and vmmd node charges it, so the plan
// rate holds across processes (event-driven hunt #16).
type SharedPublishRate interface {
	ConsumeEventPublishRate(ctx context.Context, accountID string, plan api.Plan, limits api.EventPublishLimits, n int) (bool, error)
}

// AllowShared is Allow, then the store's shared counter when store has one.
// The process bucket refuses a burst without a database round trip; the
// shared counter refuses what the processes together admit past the plan
// rate. A shared counter that cannot be read admits on the process bucket
// alone: the publish that follows needs the same database.
func (l *PublishLimiter) AllowShared(ctx context.Context, store any, accountID string, plan api.Plan, limits api.EventPublishLimits, n int) (time.Duration, bool) {
	if wait, ok := l.Allow(accountID, limits, n); !ok {
		return wait, false
	}
	shared, ok := store.(SharedPublishRate)
	if !ok || limits.PerSecond <= 0 || n <= 0 {
		return 0, true
	}
	granted, err := shared.ConsumeEventPublishRate(ctx, accountID, plan, limits, n)
	if err != nil || granted {
		return 0, true
	}
	return max(time.Second, time.Duration(float64(time.Second)*float64(n)/float64(limits.PerSecond))), false
}

// PublishRateProblem is AllowShared's answer for an HTTP route: nil when the
// events may be published, else the 429 to return.
func (l *PublishLimiter) PublishRateProblem(ctx context.Context, store any, accountID string, plan api.Plan, limits api.EventPublishLimits, n int) *api.Problem {
	if wait, ok := l.AllowShared(ctx, store, accountID, plan, limits, n); !ok {
		return api.ErrEventPublishRateLimited(limits.PerSecond, n, wait)
	}
	return nil
}

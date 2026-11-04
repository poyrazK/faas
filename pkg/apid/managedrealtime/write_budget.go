package managedrealtime

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeBudgetAccounts bounds the per-account buckets one apid keeps; the map
// is cleared when full, which only refills buckets early.
const writeBudgetAccounts = 10_000

// writeBudgetPlanTTL is how long a bucket trusts the plan it last looked up
// for a caller that is not an authenticated account (a publisher credential
// or a schedule), so those writes do not read the account on every message.
const writeBudgetPlanTTL = time.Minute

// writeBudget applies an account's RealtimeLimits.RetainedWritesPerSecond to
// every path that writes realtime rows on control-plane Postgres (ADR-1323,
// amendment of 2026-11-02). It is per apid, like the account API budget.
type writeBudget struct {
	// now is the budget's clock; nil is time.Now.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*writeBucket
}

type writeBucket struct {
	limiter  *rate.Limiter
	limits   api.RealtimeLimits
	planSeen time.Time
}

func (b *writeBudget) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// take takes n writes from the account's bucket. When the bucket cannot cover
// them now it takes nothing and reports how long until it can. A plan without
// a rate admits everything. fresh says limits were read from the account just
// now, so cachedLimits may trust them for another writeBudgetPlanTTL.
func (b *writeBudget) take(accountID string, limits api.RealtimeLimits, n int, fresh bool) (time.Duration, bool) {
	if limits.RetainedWritesPerSecond <= 0 || n <= 0 {
		return 0, true
	}
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket := b.bucketLocked(accountID, limits, now)
	if fresh {
		bucket.planSeen = now
	}
	reservation := bucket.limiter.ReserveN(now, n)
	if !reservation.OK() {
		// More writes than the bucket ever holds: wait for a full bucket.
		return time.Duration(float64(time.Second) * float64(bucket.limiter.Burst()) / float64(limits.RetainedWritesPerSecond)), false
	}
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

func (b *writeBudget) bucketLocked(accountID string, limits api.RealtimeLimits, now time.Time) *writeBucket {
	if b.buckets == nil || len(b.buckets) >= writeBudgetAccounts {
		b.buckets = map[string]*writeBucket{}
	}
	bucket := b.buckets[accountID]
	if bucket == nil || bucket.limits != limits {
		bucket = &writeBucket{limiter: rate.NewLimiter(rate.Limit(limits.RetainedWritesPerSecond), max(limits.RetainedWriteBurst, 1)), limits: limits}
		b.buckets[accountID] = bucket
	}
	return bucket
}

// cachedLimits returns the limits a bucket learned within writeBudgetPlanTTL.
func (b *writeBudget) cachedLimits(accountID string) (api.RealtimeLimits, bool) {
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket := b.buckets[accountID]
	if bucket == nil || now.Sub(bucket.planSeen) > writeBudgetPlanTTL {
		return api.RealtimeLimits{}, false
	}
	return bucket.limits, true
}

// realtimeWriteProblem takes n writes for an authenticated account: nil when
// they may proceed, else the 429 to return.
func (s *Service) realtimeWriteProblem(acct state.Account, n int) *api.Problem {
	limits, _ := api.RealtimeLimitsFor(acct.Plan)
	if wait, ok := s.writeBudget.take(acct.ID, limits, n, true); !ok {
		return api.ErrRealtimeWriteRateLimited(limits.RetainedWritesPerSecond, n, wait)
	}
	return nil
}

// allowRealtimeWrites writes the 429 and reports false when the account is
// over its realtime write rate.
func (s *Service) allowRealtimeWrites(w http.ResponseWriter, acct state.Account, n int) bool {
	if problem := s.realtimeWriteProblem(acct, n); problem != nil {
		// Nothing was written, so a keyed publish must not replay this 429.
		if capture, ok := w.(*publishCaptureWriter); ok {
			capture.unwritten = true
		}
		api.WriteProblem(w, problem)
		return false
	}
	return true
}

// takeAccountWrites takes n writes for an account known only by ID (a
// publisher credential's or a schedule's), reading its plan at most once per
// writeBudgetPlanTTL.
func (s *Service) takeAccountWrites(ctx context.Context, accountID string, n int) (api.RealtimeLimits, time.Duration, bool, error) {
	limits, cached := s.writeBudget.cachedLimits(accountID)
	if !cached {
		acct, err := s.Host.Store().AccountByID(ctx, accountID)
		if err != nil {
			return limits, 0, false, err
		}
		limits, _ = api.RealtimeLimitsFor(acct.Plan)
	}
	wait, ok := s.writeBudget.take(accountID, limits, n, !cached)
	return limits, wait, ok, nil
}

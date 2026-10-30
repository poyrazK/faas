package apidserver

import (
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// apiRequestBudget is the per-account token bucket in front of authenticated
// API routes: capacity api.Limits.APIRequestsPerMinute, refilled over a
// minute. Only failed authentications per client IP were limited, so one
// valid key could drive any endpoint, including full-table reads, at any rate
// (bug hunt 2026-10-28 H42). Per apid process.
type apiRequestBudget struct {
	mu      sync.Mutex
	now     func() time.Time
	buckets map[string]*apiRequestBucket
	swept   time.Time
}

type apiRequestBucket struct {
	tokens float64
	cap    int
	last   time.Time
}

func newAPIRequestBudget() *apiRequestBudget {
	return &apiRequestBudget{now: time.Now, buckets: map[string]*apiRequestBucket{}}
}

// take spends one request of accountID's budget of capacity a minute. It
// returns the wait before the next request when refused.
func (b *apiRequestBudget) take(accountID string, capacity int) (time.Duration, bool) {
	if b == nil || capacity <= 0 {
		return 0, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.Sub(b.swept) > time.Minute {
		// A bucket idle for a minute is full again; forget it.
		for id, bucket := range b.buckets {
			if now.Sub(bucket.last) > time.Minute {
				delete(b.buckets, id)
			}
		}
		b.swept = now
	}
	bucket := b.buckets[accountID]
	if bucket == nil {
		bucket = &apiRequestBucket{tokens: float64(capacity), cap: capacity, last: now}
		b.buckets[accountID] = bucket
	}
	if bucket.cap != capacity {
		// A plan change resizes the bucket, keeping its fill ratio.
		bucket.tokens = bucket.tokens * float64(capacity) / float64(bucket.cap)
		bucket.cap = capacity
	}
	perSecond := float64(capacity) / 60
	bucket.tokens = math.Min(float64(capacity), bucket.tokens+now.Sub(bucket.last).Seconds()*perSecond)
	bucket.last = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return 0, true
	}
	return time.Duration((1 - bucket.tokens) / perSecond * float64(time.Second)), false
}

// requireAPIRequestBudget refuses a request past the account's plan budget.
func (s *server) requireAPIRequestBudget(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		limits, ok := api.LimitsFor(acct.Plan)
		if ok {
			if wait, allowed := s.apiRequests.take(acct.ID, limits.APIRequestsPerMinute); !allowed {
				api.WriteProblem(w, api.ErrAPIRateLimited(limits.APIRequestsPerMinute, int(math.Ceil(wait.Seconds()))))
				return
			}
		}
		next(w, r, acct)
	}
}

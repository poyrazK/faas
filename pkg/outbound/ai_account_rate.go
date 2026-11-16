package outbound

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiAccountRates bounds one account's AI calls per second across all its
// integrations (ADR 20261027132936 amendment 2). Each integration keeps its
// own admission; this in-process bucket only stops one account from
// multiplying its load on the gateway's shared state by opening many
// integrations. A refused call never reaches admission.
type aiAccountRates struct {
	mu      sync.Mutex
	buckets map[string]*aiAccountBucket
}

type aiAccountBucket struct {
	tokens float64
	last   time.Time
}

// aiAccountRatesMaxIdle bounds the buckets kept: a full bucket is the same
// as none, so the oldest full ones are dropped first.
const aiAccountRatesMaxIdle = 10_000

// allow takes one call from the account's bucket. A plan without a ceiling
// is not limited here.
func (a *aiAccountRates) allow(accountID string, plan api.Plan, now time.Time) bool {
	limits, ok := api.LimitsFor(plan)
	if !ok || limits.OutboundAIAccountRatePerSecondMax <= 0 || accountID == "" {
		return true
	}
	rate := float64(limits.OutboundAIAccountRatePerSecondMax)
	burst := 2 * rate
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.buckets == nil {
		a.buckets = make(map[string]*aiAccountBucket)
	}
	bucket, ok := a.buckets[accountID]
	if !ok {
		if len(a.buckets) >= aiAccountRatesMaxIdle {
			a.dropFull(now, rate, burst)
		}
		bucket = &aiAccountBucket{tokens: burst, last: now}
		a.buckets[accountID] = bucket
	}
	if elapsed := now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = min(burst, bucket.tokens+elapsed*rate)
		bucket.last = now
	}
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

// dropFull forgets buckets that have refilled; their accounts start full
// again either way.
func (a *aiAccountRates) dropFull(now time.Time, rate, burst float64) {
	for accountID, bucket := range a.buckets {
		if bucket.tokens+now.Sub(bucket.last).Seconds()*rate >= burst {
			delete(a.buckets, accountID)
		}
	}
}

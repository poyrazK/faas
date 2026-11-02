package objectstorage

import (
	"errors"
	"net/http"
	"strconv"
	"sync"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// publicCacheControl lets a CDN or browser keep a public object for a bounded
// time and then revalidate it, so deleting or replacing it takes effect within
// PublicObjectReadMaxAgeSeconds (H9-S144). Mount paths are mutable keys; the
// earlier one-year immutable lifetime kept deleted files public.
var publicCacheControl = "public, max-age=" + strconv.Itoa(api.PublicObjectReadMaxAgeSeconds) + ", must-revalidate"

// keyedLimiters holds one token bucket per key, bounded to max keys; when
// full it starts over rather than grow, which at worst lets a key burst again.
type keyedLimiters struct {
	mu      sync.Mutex
	rate    rate.Limit
	burst   int
	max     int
	buckets map[string]*rate.Limiter
}

func newKeyedLimiters(r rate.Limit, burst, max int) *keyedLimiters {
	return &keyedLimiters{rate: r, burst: burst, max: max, buckets: map[string]*rate.Limiter{}}
}

func (l *keyedLimiters) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	limiter, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.max {
			clear(l.buckets)
		}
		limiter = rate.NewLimiter(l.rate, l.burst)
		l.buckets[key] = limiter
	}
	return limiter.Allow()
}

// publicReadLimiters holds one token bucket per public bucket.
type publicReadLimiters = keyedLimiters

func newPublicReadLimiters() *publicReadLimiters {
	return newKeyedLimiters(api.PublicObjectReadRatePerBucket, api.PublicObjectReadBurstPerBucket, api.MaxPublicObjectReadLimiters)
}

// AccountRequestLimiter bounds each bucket owner's authenticated S3 requests
// per gateway process (ADR-20261030075301).
type AccountRequestLimiter struct{ limiters *keyedLimiters }

func NewAccountRequestLimiter() *AccountRequestLimiter {
	return &AccountRequestLimiter{limiters: newKeyedLimiters(api.ObjectStorageAccountRequestsPerSecond, api.ObjectStorageAccountRequestBurst, api.MaxObjectStorageAccountLimiters)}
}

// Allow takes one token from the owner of b: its organization, which is the
// account's personal organization for a personal bucket.
func (l *AccountRequestLimiter) Allow(b state.ObjectBucket) bool {
	owner := b.OrgID
	if owner == "" {
		owner = b.AccountID
	}
	return l.limiters.allow(owner)
}

// writePublicReadRefusal answers an admission or reservation failure. A
// reached budget is the account's own limit, not an outage.
func writePublicReadRefusal(w http.ResponseWriter, err error) {
	var limit *state.ObjectStorageLimitError
	if errors.As(err, &limit) && limit.Kind == "account_bytes" {
		// ADR-20261101214152: the owner stores more than its plan's ceiling.
		http.Error(w, "this bucket's owner stores more than its plan allows; downloads resume once it is back under", http.StatusPaymentRequired)
		return
	}
	if errors.Is(err, state.ErrObjectBudget) {
		http.Error(w, "this bucket's public read allowance for the month is used up", http.StatusTooManyRequests)
		return
	}
	http.Error(w, "object storage usage is temporarily unavailable", http.StatusServiceUnavailable)
}

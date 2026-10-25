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

// publicReadLimiters holds one token bucket per public bucket, bounded to
// MaxPublicObjectReadLimiters; when full it starts over rather than grow, which
// at worst lets a bucket burst again.
type publicReadLimiters struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

func newPublicReadLimiters() *publicReadLimiters {
	return &publicReadLimiters{buckets: map[string]*rate.Limiter{}}
}

func (l *publicReadLimiters) allow(bucket string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	limiter, ok := l.buckets[bucket]
	if !ok {
		if len(l.buckets) >= api.MaxPublicObjectReadLimiters {
			clear(l.buckets)
		}
		limiter = rate.NewLimiter(api.PublicObjectReadRatePerBucket, api.PublicObjectReadBurstPerBucket)
		l.buckets[bucket] = limiter
	}
	return limiter.Allow()
}

// writePublicReadRefusal answers an admission or reservation failure. A
// reached budget is the account's own limit, not an outage.
func writePublicReadRefusal(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrObjectBudget) {
		http.Error(w, "this bucket's public read allowance for the month is used up", http.StatusTooManyRequests)
		return
	}
	http.Error(w, "object storage usage is temporarily unavailable", http.StatusServiceUnavailable)
}

package product

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// customerRateLimited is a refusal the customer may retry after a while: the
// product's login service answered 429, or apid's own per-email budget is
// used up (bug hunt H81). It is a 429 with Retry-After, not the 503 a failed
// login service gets.
type customerRateLimited struct{ retryAfter time.Duration }

func (e customerRateLimited) Error() string { return "customer sign-in rate limited" }

// retryAfterSeconds is the response's Retry-After in whole seconds, at
// least one.
func (e customerRateLimited) retryAfterSeconds() string {
	return strconv.Itoa(max(1, int((e.retryAfter+time.Second-1)/time.Second)))
}

// upstreamRetryAfter reads a Retry-After in seconds, defaulting to a minute.
func upstreamRetryAfter(value string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 && n <= 3600 {
		return time.Duration(n) * time.Second
	}
	return time.Minute
}

// customerSignInLimiter counts failed portal sign-ins per product and email
// address. Every end user's sign-in reaches the product's login service from
// apid's address, so that service cannot tell one guesser from another; apid
// limits guessing against one customer itself (bug hunt H81).
type customerSignInLimiter struct {
	mu       sync.Mutex
	failures map[string]customerSignInFailures
}

type customerSignInFailures struct {
	start time.Time
	count int
}

func customerSignInKey(productAppID, email string) string {
	return productAppID + "\x00" + strings.ToLower(strings.TrimSpace(email))
}

// blocked reports how long key must wait, zero when it may try.
func (l *customerSignInLimiter) blocked(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.failures[key]
	if !ok || f.count < api.ProductPortalSignInFailuresPerEmail {
		return 0
	}
	if wait := f.start.Add(api.ProductPortalSignInFailureWindow).Sub(now); wait > 0 {
		return wait
	}
	return 0
}

// fail counts one failed sign-in for key.
func (l *customerSignInLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failures == nil {
		l.failures = make(map[string]customerSignInFailures)
	}
	f, ok := l.failures[key]
	if !ok || now.Sub(f.start) >= api.ProductPortalSignInFailureWindow {
		if !ok && len(l.failures) >= api.ProductPortalSignInFailureKeysMax {
			l.evictLocked(now)
		}
		f = customerSignInFailures{start: now}
	}
	f.count++
	l.failures[key] = f
}

// succeed forgets key's failures.
func (l *customerSignInLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// evictLocked drops finished windows, then the keys with the fewest
// failures in a sample, so a flood of new addresses cannot reset a
// guessed-at customer's count.
func (l *customerSignInLimiter) evictLocked(now time.Time) {
	for k, f := range l.failures {
		if now.Sub(f.start) >= api.ProductPortalSignInFailureWindow {
			delete(l.failures, k)
		}
	}
	for len(l.failures) >= api.ProductPortalSignInFailureKeysMax {
		victim, fewest, seen := "", 0, 0
		for k, f := range l.failures {
			if victim == "" || f.count < fewest {
				victim, fewest = k, f.count
			}
			if seen++; seen >= 32 {
				break
			}
		}
		delete(l.failures, victim)
	}
}

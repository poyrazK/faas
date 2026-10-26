package events

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261026140000
func TestPublishLimiterChargesEveryEventPerAccount(t *testing.T) {
	var limiter PublishLimiter
	limits := api.EventPublishLimits{PerSecond: 5, Burst: 100}
	if _, ok := limiter.Allow("a", limits, 100); !ok {
		t.Fatal("a full batch within the burst was refused")
	}
	wait, ok := limiter.Allow("a", limits, 1)
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("one more event = %v, %v; want a refusal with a sub-second wait", wait, ok)
	}
	if _, ok := limiter.Allow("b", limits, 1); !ok {
		t.Fatal("another account was charged for the first one's events")
	}
	if wait, ok := limiter.Allow("c", limits, 101); ok || wait < 19*time.Second {
		t.Fatalf("more events than the burst = %v, %v; want a refusal until the bucket is full", wait, ok)
	}
	// A refusal takes nothing: a later event that fits still passes.
	if _, ok := limiter.Allow("c", limits, 100); !ok {
		t.Fatal("the refused oversize request was charged")
	}
	// A plan change gives the account a bucket at its new rate.
	if _, ok := limiter.Allow("a", api.EventPublishLimits{PerSecond: 20, Burst: 200}, 150); !ok {
		t.Fatal("the upgraded account kept its old bucket")
	}
	if _, ok := limiter.Allow("a", api.EventPublishLimits{}, 1000); !ok {
		t.Fatal("a zero rate limited the account")
	}
	var unset *PublishLimiter
	if _, ok := unset.Allow("a", limits, 1000); !ok {
		t.Fatal("a nil limiter limited the account")
	}
	problem := limiter.PublishRateProblem("b", api.EventPublishLimits{PerSecond: 5, Burst: 1}, 1)
	if problem == nil {
		problem = limiter.PublishRateProblem("b", api.EventPublishLimits{PerSecond: 5, Burst: 1}, 1)
	}
	if problem == nil || problem.Status != http.StatusTooManyRequests || problem.Code != api.CodeEventPublishRateLimited {
		t.Fatalf("problem = %+v", problem)
	}
}

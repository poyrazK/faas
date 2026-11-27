package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261119102741 §4: cohorts are cached per deployment and customer,
// "-" marks a customer in none, a failed lookup stamps nothing for a short
// while, and analytics_cohorts_changed empties the cache.
func TestFlagCohortCacheCachesInvalidatesAndRetries(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	members := map[string][]string{"cust-1": {"power", "beta"}}
	c := NewFlagCohortCache(func(_ context.Context, deploymentID, customerID string) ([]string, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("db down")
		}
		return members[customerID], nil
	})
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if got := c.Header(ctx, "dep-1", "cust-1"); got != "beta,power" {
		t.Fatalf("header = %q", got)
	}
	if got := c.Header(ctx, "dep-1", "cust-2"); got != "-" {
		t.Fatalf("header for a customer in no cohort = %q; want -", got)
	}
	c.Header(ctx, "dep-1", "cust-1")
	if calls.Load() != 2 {
		t.Fatalf("cached lookup reloaded: %d calls", calls.Load())
	}
	if got := c.Header(ctx, "dep-1", ""); got != "" {
		t.Fatalf("header without a customer = %q", got)
	}
	members["cust-1"] = []string{"churned"}
	c.InvalidateAll()
	if got := c.Header(ctx, "dep-1", "cust-1"); got != "churned" {
		t.Fatalf("header after invalidation = %q", got)
	}
	fail.Store(true)
	now = now.Add(flagCohortsTTL + time.Second)
	if got := c.Header(ctx, "dep-1", "cust-1"); got != "" {
		t.Fatalf("header on a failed lookup = %q; want none", got)
	}
	fail.Store(false)
	now = now.Add(flagCohortsRetryAfter + time.Second)
	if got := c.Header(ctx, "dep-1", "cust-1"); got != "churned" {
		t.Fatalf("header after retry = %q", got)
	}
}

func TestFlagCohortsHeaderIsAGuestIdentityHeader(t *testing.T) {
	h := http.Header{}
	h.Set(api.FlagCohortsHeader, "forged")
	api.PlatformIdentity{FlagCohorts: "beta"}.ApplyGuestHeaders(h)
	if got := h.Get(api.FlagCohortsHeader); got != "beta" {
		t.Fatalf("header = %q; want the stamped value", got)
	}
	h = http.Header{}
	h.Set(api.FlagCohortsHeader, "forged")
	api.ClearGuestIdentityHeaders(h)
	if got := h.Get(api.FlagCohortsHeader); got != "" {
		t.Fatalf("a client-authored cohort header survived: %q", got)
	}
}

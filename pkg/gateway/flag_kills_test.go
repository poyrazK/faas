package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261114150000 §3: the kill list is cached per deployment, emptied on
// feature_flags_changed, and a failed lookup stamps nothing for a short while.
func TestFlagKillCacheCachesInvalidatesAndRetries(t *testing.T) {
	var calls atomic.Int32
	kills := []string{"zeta", "alpha"}
	var fail atomic.Bool
	c := NewFlagKillCache(func(ctx context.Context, deploymentID string) ([]string, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("db down")
		}
		return kills, nil
	})
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if got := c.Header(ctx, "dep-1"); got != "alpha,zeta" {
		t.Fatalf("header = %q", got)
	}
	c.Header(ctx, "dep-1")
	if calls.Load() != 1 {
		t.Fatalf("cached lookup reloaded: %d calls", calls.Load())
	}
	kills = []string{"beta"}
	c.InvalidateAll()
	if got := c.Header(ctx, "dep-1"); got != "beta" || calls.Load() != 2 {
		t.Fatalf("after invalidate: %q (%d calls)", got, calls.Load())
	}
	now = now.Add(flagKillsTTL + time.Second)
	fail.Store(true)
	if got := c.Header(ctx, "dep-1"); got != "" {
		t.Fatalf("failed lookup stamped %q", got)
	}
	c.Header(ctx, "dep-1")
	if calls.Load() != 3 {
		t.Fatalf("failure not cached for retry window: %d calls", calls.Load())
	}
	now = now.Add(flagKillsRetryAfter + time.Second)
	fail.Store(false)
	if got := c.Header(ctx, "dep-1"); got != "beta" {
		t.Fatalf("after retry window: %q", got)
	}
	if got := (*FlagKillCache)(nil).Header(ctx, "dep-1"); got != "" {
		t.Fatalf("nil cache stamped %q", got)
	}
}

type flagKillBackend struct {
	Backend
	header string
}

func (b flagKillBackend) FlagKillsHeader(context.Context, string) string { return b.header }

func TestFlagKillsStampedAndClientCopyRemoved(t *testing.T) {
	h := &Handler{backend: flagKillBackend{header: "risky-path"}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(api.FlagKillsHeader, "forged")
	r = h.withFlagRequestAttributes(r)
	r, identity := stampTargetIdentity(r, App{ID: "app-1"}, Target{AppID: "app-1", DeploymentID: "dep-1"})
	if identity.FlagKills != "risky-path" || r.Header.Get(api.FlagKillsHeader) != "risky-path" {
		t.Fatalf("identity=%q header=%q", identity.FlagKills, r.Header.Get(api.FlagKillsHeader))
	}
	h = &Handler{}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set(api.FlagKillsHeader, "forged")
	r = h.withFlagRequestAttributes(r)
	r, _ = stampTargetIdentity(r, App{ID: "app-1"}, Target{AppID: "app-1"})
	if got := r.Header.Values(api.FlagKillsHeader); len(got) != 0 {
		t.Fatalf("client kill header survived: %v", got)
	}
}

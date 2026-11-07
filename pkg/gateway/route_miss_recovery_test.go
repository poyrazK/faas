// adr: 520 — bounded recovery of custom-domain routes after a restart.
package gateway

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/knownhosts"
)

type recentRouteSource struct{}

func (recentRouteSource) KnownRouteHosts(context.Context, int) ([]string, []string, error) {
	return nil, nil, nil
}

func (recentRouteSource) ListRecentCustomDomainTLSHosts(context.Context, int) ([]string, error) {
	return []string{"recent.example.com"}, nil
}

func TestRouteMissBudgetRecoversRecentHost(t *testing.T) {
	known := knownhosts.New("gregale.dev")
	if err := known.Refresh(t.Context(), recentRouteSource{}); err != nil {
		t.Fatal(err)
	}
	guard := newRouteMissGuard()
	guard.budget = rate.NewLimiter(0, 0)
	guard.preloaded = known
	if err := guard.admit(t.Context(), "recent.example.com"); err != nil {
		t.Fatalf("recent verified host was shed: %v", err)
	}
	if err := guard.admit(t.Context(), "unknown.example.com"); !errors.Is(err, errRouteLookupShed) {
		t.Fatalf("unknown host bypassed budget: %v", err)
	}
}

func TestRouteMissBudgetBoundsLegacyWildcardRecovery(t *testing.T) {
	known := knownhosts.New("gregale.dev")
	known.Replace(nil, []string{"*.wild.example.com"})
	guard := newRouteMissGuard()
	guard.budget = rate.NewLimiter(0, 0)
	guard.wildcardBudget = rate.NewLimiter(0, 1)
	guard.preloaded = known
	if err := guard.admit(t.Context(), "legacy.wild.example.com"); err != nil {
		t.Fatalf("legacy wildcard should use its recovery budget: %v", err)
	}
	if err := guard.admit(t.Context(), "another.wild.example.com"); !errors.Is(err, errRouteLookupShed) {
		t.Fatalf("wildcard recovery must remain bounded: %v", err)
	}
	if err := guard.admit(t.Context(), "unrelated.example.com"); !errors.Is(err, errRouteLookupShed) {
		t.Fatalf("unrelated host used wildcard recovery: %v", err)
	}
}

package gateway

// Route miss guard (ADR-20261103163954 §4).
//
// PGBackend caches only the routes it finds. A hostname that routes nowhere
// was read from the store again on every request, and concurrent requests
// shared a read only when they named the same host, so a flood of random
// names under the apps wildcard became a flood of control-plane Postgres
// reads. The guard answers a name the store just said routes nowhere from
// memory, and spends a per-gateway budget on names this gateway has never
// seen route. Past the budget the read is shed: the request is answered 503
// with Retry-After instead of reading the store.

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/knownhosts"
)

// errRouteLookupShed is a route read refused by the new-host budget. The
// host policy lookup surfaces it as 503 traffic_policy_unavailable, never as
// 404: the host may be a real app this gateway has not been asked for yet.
var errRouteLookupShed = errors.New("gateway: route lookup shed: new-host lookup budget spent")

type routeMissGuard struct {
	mu      sync.Mutex
	missing map[string]time.Time // host -> when its remembered miss expires
	known   map[string]struct{}  // hosts this gateway has seen route
	budget  *rate.Limiter
	// preloaded holds every app slug and verified custom domain, refreshed
	// from the store, so a real app's host skips the budget even before
	// this gateway has served it (after a restart). Set before serving.
	preloaded *knownhosts.Set

	missTTL    time.Duration
	maxWait    time.Duration
	maxMissing int
	maxKnown   int
	now        func() time.Time
}

func newRouteMissGuard() *routeMissGuard {
	return &routeMissGuard{
		missing:    map[string]time.Time{},
		known:      map[string]struct{}{},
		budget:     rate.NewLimiter(rate.Limit(api.GatewayNewHostLookupsPerSecond), api.GatewayNewHostLookupBurst),
		missTTL:    api.GatewayRouteMissTTL,
		maxWait:    api.GatewayNewHostLookupMaxWait,
		maxMissing: api.GatewayRouteMissMaxHosts,
		maxKnown:   api.GatewayRouteKnownMaxHosts,
		now:        time.Now,
	}
}

// missed reports whether the store said host routes nowhere within missTTL.
func (g *routeMissGuard) missed(host string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	expires, ok := g.missing[host]
	if !ok {
		return false
	}
	if !g.now().Before(expires) {
		delete(g.missing, host)
		return false
	}
	return true
}

// admit lets a store read of host go ahead. A host this gateway has seen
// route always may; any other spends the new-host budget, waiting up to
// maxWait for it.
func (g *routeMissGuard) admit(ctx context.Context, host string) error {
	if g == nil {
		return nil
	}
	if g.preloaded.Has(host) {
		return nil
	}
	g.mu.Lock()
	_, seen := g.known[host]
	g.mu.Unlock()
	if seen {
		return nil
	}
	now := g.now()
	reservation := g.budget.ReserveN(now, 1)
	delay := reservation.DelayFrom(now)
	if !reservation.OK() || delay > g.maxWait {
		reservation.CancelAt(now)
		return errRouteLookupShed
	}
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		reservation.Cancel()
		return ctx.Err()
	}
}

// found records that host routes: it skips the budget from now on.
func (g *routeMissGuard) found(host string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.missing, host)
	if _, ok := g.known[host]; ok {
		return
	}
	if len(g.known) >= g.maxKnown {
		g.known = map[string]struct{}{}
	}
	g.known[host] = struct{}{}
}

// notFound records the store's answer that host routes nowhere.
func (g *routeMissGuard) notFound(host string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.known, host)
	if len(g.missing) >= g.maxMissing {
		g.missing = map[string]time.Time{}
	}
	g.missing[host] = g.now().Add(g.missTTL)
}

// forget drops host's remembered miss.
func (g *routeMissGuard) forget(host string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.missing, host)
	g.mu.Unlock()
}

// forgetMisses drops every remembered miss: a route invalidation may have
// made any of those hosts route.
func (g *routeMissGuard) forgetMisses() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.missing = map[string]time.Time{}
	g.mu.Unlock()
}

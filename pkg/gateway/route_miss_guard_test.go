// spec: §4.1 — a hostname that routes nowhere is not a control-plane read per request.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"
)

// countingHostRouter routes the hosts in apps and counts every read. A host
// in dynamic is resolved on every request, like a stage URL. gate, when set,
// holds each read until it is closed.
type countingHostRouter struct {
	mu      sync.Mutex
	apps    map[string]App
	dynamic map[string]bool
	reads   map[string]int
	gate    chan struct{}
	reading chan struct{}
}

func newCountingHostRouter(apps map[string]App) *countingHostRouter {
	return &countingHostRouter{apps: apps, dynamic: map[string]bool{}, reads: map[string]int{}}
}

func (r *countingHostRouter) ResolveHost(ctx context.Context, host string) (App, bool, error) {
	r.mu.Lock()
	r.reads[host]++
	gate, reading := r.gate, r.reading
	r.mu.Unlock()
	if gate != nil {
		if reading != nil {
			reading <- struct{}{}
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return App{}, false, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	app, ok := r.apps[host]
	return app, ok, nil
}

func (r *countingHostRouter) IsDynamicRouteHost(host string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dynamic[host]
}

func (r *countingHostRouter) readsOf(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads[host]
}

func (r *countingHostRouter) totalReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.reads {
		n += c
	}
	return n
}

func (r *countingHostRouter) route(host string, app App) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apps[host] = app
}

// testClock is a settable clock for the guard.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// newTestRouteMissGuard is the production guard with a budget of burst
// reads that never refills and no wait, on clock.
func newTestRouteMissGuard(burst int, clock *testClock) *routeMissGuard {
	g := newRouteMissGuard()
	g.budget = rate.NewLimiter(0, burst)
	g.maxWait = 0
	g.now = clock.Now
	return g
}

func TestRouteMissIsRememberedUntilARouteInvalidation(t *testing.T) {
	cases := []struct {
		name       string
		invalidate func(*PGBackend)
	}{
		{"app changed or created", func(b *PGBackend) { b.InvalidateRoutesForApp("app-new") }},
		{"routes flushed", func(b *PGBackend) { b.FlushRoutes() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const host = "fresh.gregale.dev"
			router := newCountingHostRouter(map[string]App{})
			b := NewPGBackend(router, nil, nil)
			m := NewMetrics()
			b.WithMetrics(m)
			ctx := context.Background()
			for range 3 {
				if _, ok := b.Lookup(ctx, host); ok {
					t.Fatal("an unrouted host resolved")
				}
			}
			if n := router.readsOf(host); n != 1 {
				t.Fatalf("three lookups of an unrouted host made %d store reads, want 1", n)
			}
			if got := testutil.ToFloat64(m.routeLookupUnknownHost.WithLabelValues("remembered_miss")); got != 2 {
				t.Fatalf("remembered_miss counter = %v, want 2", got)
			}
			router.route(host, App{ID: "app-new", AccountID: "acct-1", Slug: "fresh"})
			tc.invalidate(b)
			app, ok := b.Lookup(ctx, host)
			if !ok || app.ID != "app-new" {
				t.Fatalf("after the invalidation the new app's host resolved to %+v, %v; want app-new", app, ok)
			}
		})
	}
}

func TestRememberedRouteMissExpires(t *testing.T) {
	const host = "typo.gregale.dev"
	clock := &testClock{now: time.Unix(1_800_000_000, 0)}
	router := newCountingHostRouter(map[string]App{})
	b := NewPGBackend(router, nil, nil)
	b.misses = newTestRouteMissGuard(10, clock)
	ctx := context.Background()
	b.Lookup(ctx, host)
	clock.Advance(b.misses.missTTL - time.Second)
	b.Lookup(ctx, host)
	if n := router.readsOf(host); n != 1 {
		t.Fatalf("a lookup within the miss TTL read the store again: %d reads, want 1", n)
	}
	clock.Advance(2 * time.Second)
	b.Lookup(ctx, host)
	if n := router.readsOf(host); n != 2 {
		t.Fatalf("a lookup after the miss TTL made %d reads in all, want 2", n)
	}
}

// A flood of random names spends the new-host budget and then gets
// errRouteLookupShed (503 at the handler) without store reads, while hosts
// this gateway has seen route keep resolving.
func TestNewHostBudgetShedsUnseenHostsOnly(t *testing.T) {
	const known = "pulse.gregale.dev"
	clock := &testClock{now: time.Unix(1_800_000_000, 0)}
	router := newCountingHostRouter(map[string]App{
		known: {ID: "app-1", AccountID: "acct-1", Slug: "pulse"},
	})
	b := NewPGBackend(router, nil, nil)
	b.misses = newTestRouteMissGuard(3, clock)
	m := NewMetrics()
	b.WithMetrics(m)
	ctx := context.Background()
	if _, ok, err := b.LookupHostPolicy(ctx, known); !ok || err != nil {
		t.Fatalf("known host: ok=%v err=%v", ok, err)
	}
	var shed int
	for i := range 50 {
		_, ok, err := b.LookupHostPolicy(ctx, fmt.Sprintf("r%d.gregale.dev", i))
		switch {
		case errors.Is(err, errRouteLookupShed):
			shed++
		case err != nil || ok:
			t.Fatalf("random name %d: ok=%v err=%v", i, ok, err)
		}
	}
	if shed != 48 {
		t.Fatalf("%d of 50 random names were shed, want 48 (budget 3, one spent on the known host)", shed)
	}
	if n := router.totalReads(); n != 3 {
		t.Fatalf("the flood made %d store reads in all, want 3", n)
	}
	if got := testutil.ToFloat64(m.routeLookupUnknownHost.WithLabelValues("shed")); got != 48 {
		t.Fatalf("shed counter = %v, want 48", got)
	}
	// The known host left the route cache, as after an app change.
	b.InvalidateRoutesForApp("app-1")
	if app, ok, err := b.LookupHostPolicy(ctx, known); !ok || err != nil || app.ID != "app-1" {
		t.Fatalf("known host during the flood: app=%+v ok=%v err=%v", app, ok, err)
	}
	if _, ok := b.Lookup(ctx, "another.gregale.dev"); ok {
		t.Fatal("Lookup resolved a shed host")
	}
}

func TestNewHostBudgetCoversDynamicHosts(t *testing.T) {
	const stage = "env-1--app-1.preview.gregale.dev"
	clock := &testClock{now: time.Unix(1_800_000_000, 0)}
	router := newCountingHostRouter(map[string]App{
		stage: {ID: "app-1", AccountID: "acct-1", Slug: "pulse", DynamicRoute: true},
	})
	router.dynamic[stage] = true
	router.dynamic["env-9--app-9.preview.gregale.dev"] = true
	b := NewPGBackend(router, nil, nil)
	b.misses = newTestRouteMissGuard(1, clock)
	ctx := context.Background()
	for i := range 3 {
		if _, ok, err := b.LookupHostPolicy(ctx, stage); !ok || err != nil {
			t.Fatalf("stage URL lookup %d: ok=%v err=%v", i, ok, err)
		}
	}
	if n := router.readsOf(stage); n != 3 {
		t.Fatalf("a dynamic host is read on every request: %d reads, want 3", n)
	}
	if _, _, err := b.LookupHostPolicy(ctx, "env-9--app-9.preview.gregale.dev"); !errors.Is(err, errRouteLookupShed) {
		t.Fatalf("unseen dynamic host past the budget: err=%v, want errRouteLookupShed", err)
	}
}

// A miss read before an invalidation landed may predate the route that
// invalidation announced, so it is not remembered.
func TestRouteMissOvertakenByInvalidationIsNotRemembered(t *testing.T) {
	const host = "racing.gregale.dev"
	router := newCountingHostRouter(map[string]App{})
	router.gate = make(chan struct{})
	router.reading = make(chan struct{}, 1)
	b := NewPGBackend(router, nil, nil)
	done := make(chan bool)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, ok := b.Lookup(ctx, host)
		done <- ok
	}()
	<-router.reading
	router.route(host, App{ID: "app-new", AccountID: "acct-1", Slug: "racing"})
	b.InvalidateRoutesForApp("app-new")
	router.mu.Lock()
	// The read in flight answers from before the route existed.
	delete(router.apps, host)
	router.mu.Unlock()
	close(router.gate)
	if <-done {
		t.Fatal("the read in flight resolved")
	}
	router.route(host, App{ID: "app-new", AccountID: "acct-1", Slug: "racing"})
	if _, ok := b.Lookup(context.Background(), host); !ok {
		t.Fatal("a miss read before the invalidation hid the new route")
	}
}

func TestRouteMissGuardBounds(t *testing.T) {
	cases := []struct {
		name  string
		fill  func(*routeMissGuard, int)
		count func(*routeMissGuard) int
		cap   func(*routeMissGuard) *int
	}{
		{
			name:  "remembered misses",
			fill:  func(g *routeMissGuard, i int) { g.notFound(fmt.Sprintf("m%d.gregale.dev", i)) },
			count: func(g *routeMissGuard) int { return len(g.missing) },
			cap:   func(g *routeMissGuard) *int { return &g.maxMissing },
		},
		{
			name:  "known hosts",
			fill:  func(g *routeMissGuard, i int) { g.found(fmt.Sprintf("k%d.gregale.dev", i)) },
			count: func(g *routeMissGuard) int { return len(g.known) },
			cap:   func(g *routeMissGuard) *int { return &g.maxKnown },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestRouteMissGuard(1, &testClock{now: time.Unix(1_800_000_000, 0)})
			*tc.cap(g) = 4
			for i := range 9 {
				tc.fill(g, i)
				if n := tc.count(g); n > 4 {
					t.Fatalf("after %d inserts the set holds %d hosts, cap 4", i+1, n)
				}
			}
		})
	}
}

func TestRouteMissGuardFoundAndNotFoundReplaceEachOther(t *testing.T) {
	g := newTestRouteMissGuard(0, &testClock{now: time.Unix(1_800_000_000, 0)})
	const host = "flip.gregale.dev"
	g.notFound(host)
	g.found(host)
	if g.missed(host) {
		t.Fatal("a host that now routes is still a remembered miss")
	}
	if err := g.admit(context.Background(), host); err != nil {
		t.Fatalf("a host seen routing spent the empty budget: %v", err)
	}
	g.notFound(host)
	if err := g.admit(context.Background(), host); !errors.Is(err, errRouteLookupShed) {
		t.Fatalf("a host that stopped routing skipped the budget: %v", err)
	}
}

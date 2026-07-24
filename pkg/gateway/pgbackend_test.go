package gateway_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// fakeRouter is a controllable gateway.Router.
type fakeRouter struct {
	mu    sync.Mutex
	byID  map[string]gateway.App // host -> app
	calls int
	err   error
}

func (r *fakeRouter) ResolveHost(_ context.Context, host string) (gateway.App, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return gateway.App{}, false, r.err
	}
	app, ok := r.byID[host]
	return app, ok, nil
}

func (r *fakeRouter) resolveCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestPGBackend_LookupCachesAndFallsBack(t *testing.T) {
	router := &fakeRouter{byID: map[string]gateway.App{
		"a.apps.example.com": {ID: "app-1", Plan: api.PlanPro},
	}}
	b := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)

	// Miss → Router resolves and caches.
	app, ok := b.Lookup(context.Background(), "a.apps.example.com")
	if !ok || app.ID != "app-1" || app.Plan != api.PlanPro {
		t.Fatalf("first lookup = %+v ok=%v", app, ok)
	}
	// Hit → no second Router call.
	if _, ok := b.Lookup(context.Background(), "a.apps.example.com"); !ok {
		t.Fatal("second lookup missed")
	}
	if n := router.resolveCalls(); n != 1 {
		t.Errorf("router resolve calls = %d, want 1 (cached)", n)
	}
}

func TestPGBackend_LookupUnknownHost(t *testing.T) {
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil)
	if _, ok := b.Lookup(context.Background(), "nope.example.com"); ok {
		t.Fatal("unknown host resolved")
	}
}

func TestPGBackend_LookupRouterErrorIsNotFound(t *testing.T) {
	router := &fakeRouter{err: errors.New("pg down")}
	b := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
	if _, ok := b.Lookup(context.Background(), "a.apps.example.com"); ok {
		t.Fatal("router error should surface as not-found, not a route")
	}
}

// TestPGBackend_AdmitSeedsThenEvictInstance (issue #168) — Admit caches
// the new instance; EvictInstance drops exactly that entry; siblings in
// the same targetSet survive.
func TestPGBackend_AdmitSeedsThenEvictInstance(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-fake-1").WithInstanceID("i-1")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	// No admit yet → no target.
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount pre-admit = %d, want 0", got)
	}
	if _, ok := b.Pick("app-1"); ok {
		t.Fatal("Pick pre-admit = ok; want empty cache")
	}

	if _, _, err := b.Admit(context.Background(), "app-1", 5); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount post-admit = %d, want 1", got)
	}
	t1, ok := b.Pick("app-1")
	if !ok || t1.InstanceID != "i-1" || t1.NodeID != "node-fake-1" {
		t.Fatalf("Pick post-admit = %+v ok=%v, want i-1/node-fake-1", t1, ok)
	}

	// EvictInstance drops the matching entry; cache becomes empty.
	b.EvictInstance("app-1", "i-1")
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount post-evict = %d, want 0", got)
	}
	if _, ok := b.Pick("app-1"); ok {
		t.Fatal("Pick post-evict = ok; want empty cache")
	}
}

// TestPGBackend_FanOutAcrossMaxConcurrency (issue #168) — three Admits
// fill the per-app targetSet; EvictInstance drops one, two remain.
func TestPGBackend_FanOutAcrossMaxConcurrency(t *testing.T) {
	// One FakeScheduler with a fixed node id but unique instance ids per
	// call (the default mints i-1, i-2, ...).
	sched := gateway.NewFakeScheduler("node-shared")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	for _, want := range []string{"i-1", "i-2", "i-3"} {
		if _, _, err := b.Admit(context.Background(), "app-1", 5); err != nil {
			t.Fatalf("Admit: %v", err)
		}
		if got := b.HealthyCount("app-1"); got == 0 {
			t.Fatalf("HealthyCount after admit = %d", got)
		}
		_ = want // instance ids come from the FakeScheduler's mint loop
	}

	if got := b.HealthyCount("app-1"); got != 3 {
		t.Fatalf("HealthyCount after 3 admits = %d, want 3", got)
	}

	// Pick round-robin: collect 3 distinct targets.
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		t1, ok := b.Pick("app-1")
		if !ok {
			t.Fatalf("Pick #%d = !ok", i)
		}
		if seen[t1.InstanceID] {
			t.Errorf("Pick round-robin returned %q twice", t1.InstanceID)
		}
		seen[t1.InstanceID] = true
	}
	if len(seen) != 3 {
		t.Errorf("distinct picks = %d, want 3", len(seen))
	}

	// Evict the middle one; two siblings remain.
	b.EvictInstance("app-1", "i-2")
	if got := b.HealthyCount("app-1"); got != 2 {
		t.Fatalf("HealthyCount after evict i-2 = %d, want 2", got)
	}
	// Pick must not return the evicted one anymore.
	for i := 0; i < 8; i++ {
		t1, ok := b.Pick("app-1")
		if !ok {
			t.Fatalf("Pick #%d = !ok", i)
		}
		if t1.InstanceID == "i-2" {
			t.Errorf("evicted instance i-2 returned by Pick")
		}
	}
}

// TestPGBackend_AdmitErrorDoesNotSeedTarget (issue #168) — a real
// admission failure (e.g. RAM headroom surfaced as *api.Problem) must
// not leak a partial target into the cache.
func TestPGBackend_AdmitErrorDoesNotSeedTarget(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-fake-1").WithErr(api.ErrCapacity("full"))
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	if _, _, err := b.Admit(context.Background(), "app-1", 5); err == nil {
		t.Fatal("expected admit error")
	}
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount after failed admit = %d, want 0", got)
	}
}

// TestPGBackend_AdmitAtCapacityIsTypedResult (issue #168) — schedd's
// "already at max_concurrency" surfaces as atCapacity=true with no error.
// The gateway treats it as a benign no-op; nothing is cached.
func TestPGBackend_AdmitAtCapacityIsTypedResult(t *testing.T) {
	sched := &atCapScheduler{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	wakeID, atCap, err := b.Admit(context.Background(), "app-1", 5)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if !atCap {
		t.Errorf("atCapacity = false; want true on app_concurrency_reached path")
	}
	if wakeID != "" {
		t.Errorf("wakeID = %q on at-capacity path; want empty", wakeID)
	}
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount after at-cap admit = %d, want 0", got)
	}
}

// atCapScheduler is a controllable Scheduler that always returns the
// typed at_capacity=true outcome (issue #168).
type atCapScheduler struct{}

func (atCapScheduler) AdmitInstance(context.Context, string) (string, string, string, bool, error) {
	return "", "", "", true, nil
}

func TestPGBackend_FlushRoutesForcesReresolve(t *testing.T) {
	router := &fakeRouter{byID: map[string]gateway.App{
		"a.apps.example.com": {ID: "app-1", Plan: api.PlanFree},
	}}
	b := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)

	if _, ok := b.Lookup(context.Background(), "a.apps.example.com"); !ok {
		t.Fatal("seed lookup failed")
	}
	b.FlushRoutes()
	if _, ok := b.Lookup(context.Background(), "a.apps.example.com"); !ok {
		t.Fatal("post-flush lookup failed")
	}
	if n := router.resolveCalls(); n != 2 {
		t.Errorf("router resolve calls = %d, want 2 (cache flushed)", n)
	}
}

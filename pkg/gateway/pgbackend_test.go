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

func TestPGBackend_WakeSeedsTargetThenEvict(t *testing.T) {
	sched := gateway.NewFakeScheduler("10.0.0.9:8080")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	// No wake yet → no target.
	if _, ok := b.Target("app-1"); ok {
		t.Fatal("target present before wake")
	}
	if _, err := b.Wake(context.Background(), "app-1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Post-#98 / ADR-028 the cached "target" is the compute_node.id
	// schedd chose, not a host:port. FakeScheduler seeds that string
	// from its `addr` constructor argument for back-compat with older
	// tests; the value flows back as if it were the synthetic node id.
	nodeID, ok := b.Target("app-1")
	if !ok || nodeID != "10.0.0.9:8080" {
		t.Fatalf("target after wake = %q ok=%v", nodeID, ok)
	}
	// The wake also records node_id→instance so the last_request_at flush can
	// attribute touches.
	if id, ok := b.InstanceIDForNodeID("10.0.0.9:8080"); !ok || id != "i-fake" {
		t.Fatalf("InstanceIDForNodeID = %q,%v, want i-fake,true", id, ok)
	}
	// instance_changed → evict; next request must re-wake, and the node→instance
	// mapping is gone (the instance parked).
	b.EvictTarget("app-1")
	if _, ok := b.Target("app-1"); ok {
		t.Fatal("target survived eviction")
	}
	if _, ok := b.InstanceIDForNodeID("10.0.0.9:8080"); ok {
		t.Fatal("nodeID→instance survived eviction")
	}
}

func TestPGBackend_WakeInstanceIDFromScheduler(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-fake-5").WithInstanceID("i-42")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)
	if _, err := b.Wake(context.Background(), "app-9"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if id, ok := b.InstanceIDForNodeID("node-fake-5"); !ok || id != "i-42" {
		t.Errorf("InstanceIDForNodeID = %q,%v, want i-42,true", id, ok)
	}
}

func TestPGBackend_WakeErrorDoesNotSeedTarget(t *testing.T) {
	sched := gateway.NewFakeScheduler("10.0.0.9:8080").WithErr(api.ErrCapacity("full"))
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)
	if _, err := b.Wake(context.Background(), "app-1"); err == nil {
		t.Fatal("expected wake error")
	}
	if _, ok := b.Target("app-1"); ok {
		t.Fatal("failed wake must not seed a target")
	}
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

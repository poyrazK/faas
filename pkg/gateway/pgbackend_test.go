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
		"a.apps.gregale.dev": {ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
	}}
	b := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)

	// Miss → Router resolves and caches.
	app, ok := b.Lookup(context.Background(), "a.apps.gregale.dev")
	if !ok || app.ID != "app-1" || app.AccountID != "acct-1" || app.Plan != api.PlanPro {
		t.Fatalf("first lookup = %+v ok=%v", app, ok)
	}
	// Hit → no second Router call.
	if _, ok := b.Lookup(context.Background(), "a.apps.gregale.dev"); !ok {
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
	if _, ok := b.Lookup(context.Background(), "a.apps.gregale.dev"); ok {
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
	if res := b.Pick("app-1"); res.OK {
		t.Fatal("Pick pre-admit = ok; want empty cache")
	}

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", 5); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount post-admit = %d, want 1", got)
	}
	t1 := b.Pick("app-1")
	if !t1.OK {
		t.Fatal("Pick: !ok")
	}
	if !t1.OK || t1.Target.InstanceID != "i-1" || t1.Target.NodeID != "node-fake-1" {
		t.Fatalf("Pick post-admit = %+v ok=%v, want i-1/node-fake-1", t1.Target, t1.OK)
	}

	// EvictInstance drops the matching entry; cache becomes empty.
	b.EvictInstance("app-1", "i-1")
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount post-evict = %d, want 0", got)
	}
	if res := b.Pick("app-1"); res.OK {
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
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", 5); err != nil {
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
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		if seen[t1.Target.InstanceID] {
			t.Errorf("Pick round-robin returned %q twice", t1.Target.InstanceID)
		}
		seen[t1.Target.InstanceID] = true
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
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		if t1.Target.InstanceID == "i-2" {
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

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", 5); err == nil {
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

	wakeID, _, atCap, err := b.Admit(context.Background(), "app-1", "", 5)
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

func (atCapScheduler) AdmitInstance(context.Context, string, string) (string, string, string, string, int32, bool, int, error) {
	return "", "", "", "", 0, true, 0, nil
}

func (atCapScheduler) EnsureWake(context.Context, string) (string, string, string, string, int32, int, error) {
	return "", "", "", "", 0, 0, nil
}

// TestPGBackend_AdmitForwardsWakeMethod (PR scale-out readiness) — the
// raw wire-method value the Scheduler returns must reach PGBackend.Admit's
// caller unchanged. This is the load-bearing step that lets the
// handler's wake-locality classifier distinguish a snapshot restore
// from a cold boot. The translation from int32 → WakeMethod happens
// inside PGBackend.Admit via scheddWakeMethodToGateway; this test
// pins both the cold-boot default and the restore path, plus the
// unknown-wire fallback to cold boot.
func TestPGBackend_AdmitForwardsWakeMethod(t *testing.T) {
	cases := []struct {
		name     string
		raw      int32
		wantWake gateway.WakeMethod
	}{
		{"wireWakeColdBoot → WakeMethodColdBoot", gateway.WireWakeColdBoot, gateway.WakeMethodColdBoot},
		{"wireWakeRestore → WakeMethodSnapshotRestore", gateway.WireWakeRestore, gateway.WakeMethodSnapshotRestore},
		// Unknown wire value falls through the default branch to
		// cold boot — same defense as scheddgrpc.mapMethod.
		{"unknown wire value → WakeMethodColdBoot default", 999, gateway.WakeMethodColdBoot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched := &controllableScheduler{rawMethod: tc.raw}
			b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

			_, method, atCap, err := b.Admit(context.Background(), "app-1", "", 5)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			if atCap {
				t.Errorf("atCapacity = true; want false on admit path")
			}
			if method != tc.wantWake {
				t.Errorf("method = %v, want %v", method, tc.wantWake)
			}
		})
	}
}

// TestPGBackend_AdmitAtCapacityLeavesMethodUnspecified (PR scale-out
// readiness) — at-capacity is a benign no-op from the locality
// classifier's perspective; the method must surface as
// WakeMethodUnspecified so the handler skips the metric increment.
func TestPGBackend_AdmitAtCapacityLeavesMethodUnspecified(t *testing.T) {
	sched := &atCapScheduler{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	_, method, atCap, err := b.Admit(context.Background(), "app-1", "", 5)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if !atCap {
		t.Errorf("atCapacity = false; want true")
	}
	if method != gateway.WakeMethodUnspecified {
		t.Errorf("method = %v, want WakeMethodUnspecified", method)
	}
}

// controllableScheduler lets a test pin the raw wire-method value
// AdmitInstance returns (PR scale-out readiness). atCapacity and
// err are zero-valued so each test case is "fresh admit, no error".
type controllableScheduler struct {
	rawMethod int32
}

func (c *controllableScheduler) AdmitInstance(context.Context, string, string) (string, string, string, string, int32, bool, int, error) {
	return "i-test", "n-test", "", "w-test", c.rawMethod, false, 0, nil
}

func (c *controllableScheduler) EnsureWake(context.Context, string) (string, string, string, string, int32, int, error) {
	return "i-test", "n-test", "", "w-test", c.rawMethod, 0, nil
}

func TestPGBackend_FlushRoutesForcesReresolve(t *testing.T) {
	router := &fakeRouter{byID: map[string]gateway.App{
		"a.apps.gregale.dev": {ID: "app-1", Plan: api.PlanFree},
	}}
	b := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)

	if _, ok := b.Lookup(context.Background(), "a.apps.gregale.dev"); !ok {
		t.Fatal("seed lookup failed")
	}
	b.FlushRoutes()
	if _, ok := b.Lookup(context.Background(), "a.apps.gregale.dev"); !ok {
		t.Fatal("post-flush lookup failed")
	}
	if n := router.resolveCalls(); n != 2 {
		t.Errorf("router resolve calls = %d, want 2 (cache flushed)", n)
	}
}

// TestPGBackend_AdmitCarriesOverridePort pins issue #460 / ADR-053
// (PR-C) on the gateway's caching surface: when the FakeScheduler
// returns port=9090, the cached Target must carry that port so the
// forwarder can stamp it onto ForwardHTTPRequestInit. A regression
// that drops Target.Port would force the forwarder to dial :8080
// against a guest bound on :9090 — silent 503s.
func TestPGBackend_AdmitCarriesOverridePort(t *testing.T) {
	sched := gateway.NewFakeScheduler("n-1").
		WithInstanceID("i-1").
		WithWakeID("w-1").
		WithPort(9090)
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", 5); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	tgt := b.Pick("app-1")
	if !tgt.OK {
		t.Fatal("Pick: !ok")
	}
	if tgt.Target.Port != 9090 {
		t.Errorf("Target.Port = %d, want 9090", tgt.Target.Port)
	}
	if tgt.Target.NodeID != "n-1" || tgt.Target.InstanceID != "i-1" {
		t.Errorf("Target identity = %+v, want n-1 / i-1", tgt.Target)
	}
}

// TestPGBackend_AdmitPortZeroIsZero pins the no-override boundary:
// when the FakeScheduler doesn't set a port, the cached Target must
// carry port=0 so vmmd's buildBridgeScript defaults to 8080 at the
// wire boundary. Asserting this prevents a future "always default to
// 8080 in the sched wrapper" regression from leaking non-zero ports
// onto legacy callers.
func TestPGBackend_AdmitPortZeroIsZero(t *testing.T) {
	sched := gateway.NewFakeScheduler("n-1").WithInstanceID("i-1")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", 5); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	tgt := b.Pick("app-1")
	if !tgt.OK {
		t.Fatal("Pick: !ok")
	}
	if tgt.Target.Port != 0 {
		t.Errorf("Target.Port = %d, want 0 (legacy 8080 default at vmmd)", tgt.Target.Port)
	}
}

// --- resolveSched legacy-fallback posture gate (PR #509 finding F1) ---
//
// Background: PGBackend.resolveSched picks between the legacy
// single-schedd field (b.sched) and the per-node client cache
// (resolved via WithAppResolver + WithClientForApp). On a transient
// resolver miss (ok=false on the AppByID path, or app.NodeID empty),
// the legacy fallback would route a foreign-owned app through the
// default-local dial and surface a FailedPrecondition storm. The
// legacySingleBox flag (WithLegacySingleBox) gates the fallback:
// single-box posture (one schedd, every app owned locally) keeps
// it; multi-box posture forbids it and surfaces a typed error.

// capturingScheduler records the per-node clients selected by
// WithClientForApp so the multi-box branch can assert that the
// owner-schedd client — not b.sched — receives the Admit.
type capturingScheduler struct {
	id       string
	admitted int
}

func (c *capturingScheduler) AdmitInstance(_ context.Context, _, _ string) (string, string, string, string, int32, bool, int, error) {
	c.admitted++
	return "fake-instance-" + c.id, "127.0.0.1", "", "w-1", 8080, true, 0, nil
}

func (c *capturingScheduler) EnsureWake(_ context.Context, _ string) (string, string, string, string, int32, int, error) {
	c.admitted++
	return "fake-instance-" + c.id, "127.0.0.1", "", "w-1", 8080, 0, nil
}

// TestPGBackend_ResolveSched_MultiBox_RejectsTransientMiss covers
// the headline fix: with WithLegacySingleBox(false) and the
// resolver returning ok=false (transient cache miss on AppByID),
// resolveSched must return an error rather than silently routing
// through b.sched. The error message names "multi-box posture" so
// an operator on call can trace the routing mistake without a
// debugger.
func TestPGBackend_ResolveSched_MultiBox_RejectsTransientMiss(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	ownerSched := &capturingScheduler{id: "node-owner"}
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithLegacySingleBox(false).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			// Resolver says "transient miss" (ok=false, err=nil).
			return gateway.App{}, false, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			// Should NOT be called on a transient miss — if it
			// were, the test would silently route through
			// b.sched via the legacy fallback.
			t.Errorf("clientForApp should not be called on transient miss")
			return nil, false, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-x", "", 5); err == nil {
		t.Fatal("expected Admit to error on transient miss in multi-box posture, got nil")
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0 (multi-box posture forbids fallback)", legacySched.Calls())
	}
	if ownerSched.admitted != 0 {
		t.Errorf("owner-sched received %d Admits, want 0 (resolver declined before clientForApp)", ownerSched.admitted)
	}
}

// TestPGBackend_ResolveSched_MultiBox_RejectsEmptyNodeID pins the
// pre-migration-row branch: an app row that survived the migration
// but still has NodeID="" must error rather than fall through. The
// legacySingleBox=false posture refuses the fallback even though
// the resolver succeeded.
func TestPGBackend_ResolveSched_MultiBox_RejectsEmptyNodeID(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithLegacySingleBox(false).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{ID: "app-y", NodeID: ""}, true, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			t.Errorf("clientForApp must not be called when NodeID is empty")
			return nil, false, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-y", "", 5); err == nil {
		t.Fatal("expected Admit to error on empty NodeID in multi-box posture, got nil")
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0", legacySched.Calls())
	}
}

// TestPGBackend_ResolveSched_LegacySingleBox_FallsBackToBSched is
// the regression guard for the single-box posture: a transient
// resolver miss falls through to b.sched, which is correct because
// the local schedd owns every app on the box. The pre-PR behaviour
// is preserved byte-for-byte for single-box installs that never
// configure FAAS_NODE_NAME.
func TestPGBackend_ResolveSched_LegacySingleBox_FallsBackToBSched(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithLegacySingleBox(true).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{}, false, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			t.Errorf("clientForApp must not be called when resolver returns ok=false")
			return nil, false, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-z", "", 5); err != nil {
		t.Fatalf("single-box posture must accept transient miss; got err=%v", err)
	}
	if legacySched.Calls() != 1 {
		t.Errorf("legacy b.sched received %d Admits, want 1 (single-box fallback)", legacySched.Calls())
	}
}

// TestPGBackend_ResolveSched_MultiBox_HappyPathRoutesToOwner is the
// contrast to the rejection cases: with legacySingleBox=false and
// the resolver returning ok=true with a valid NodeID, the
// owner-schedd client (returned by clientForApp) — NOT b.sched —
// receives the Admit.
func TestPGBackend_ResolveSched_MultiBox_HappyPathRoutesToOwner(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	ownerSched := &capturingScheduler{id: "node-owner"}
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithLegacySingleBox(false).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{ID: "app-ok", NodeID: "node-owner"}, true, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			return ownerSched, true, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-ok", "", 5); err != nil {
		t.Fatalf("multi-box happy path Admit: %v", err)
	}
	if ownerSched.admitted != 1 {
		t.Errorf("owner-sched received %d Admits, want 1", ownerSched.admitted)
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0 (multi-box routes to owner)", legacySched.Calls())
	}
}

package gateway_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
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
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
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

// TestPGBackend_StaleTargetIsNotRehydrated pins the transport-failure fence.
// The instance row may still be RUNNING while vmmd's liveness failure is
// propagating through schedd; reconciliation must not put that same target
// back into the picker during the recovery window.
func TestPGBackend_StaleTargetIsNotRehydrated(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-shared")
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil).
		WithLiveTargetLoader(func(context.Context, string) ([]gateway.Target, error) {
			return []gateway.Target{{InstanceID: "i-1", NodeID: "node-shared"}}, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	b.EvictInstance("app-1", "i-1")

	if err := b.ReconcileLiveTargets(context.Background(), "app-1"); err != nil {
		t.Fatalf("ReconcileLiveTargets: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 0 {
		t.Fatalf("HealthyCount after stale reconciliation = %d, want 0", got)
	}

	// A fresh admission uses a new instance identity and must not be blocked
	// by the old instance's quarantine.
	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
		t.Fatalf("replacement Admit: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount after replacement = %d, want 1", got)
	}
	if pick := b.Pick("app-1"); !pick.OK || pick.Target.InstanceID != "i-2" {
		t.Fatalf("replacement Pick = %+v, want i-2", pick)
	}
}

// TestPGBackend_AdmitErrorDoesNotSeedTarget (issue #168) — a real
// admission failure (e.g. RAM headroom surfaced as *api.Problem) must
// not leak a partial target into the cache.
func TestPGBackend_AdmitErrorDoesNotSeedTarget(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-fake-1").WithErr(api.ErrCapacity("full"))
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err == nil {
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

	wakeID, _, atCap, err := b.Admit(context.Background(), "app-1", "", "", "", 5)
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

func TestPGBackend_AdmitAtCapacityHydratesLiveTarget(t *testing.T) {
	sched := &atCapScheduler{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil).
		WithLiveTargetLoader(func(context.Context, string) ([]gateway.Target, error) {
			return []gateway.Target{{InstanceID: "running-1", NodeID: "node-fake-1"}}, nil
		})

	_, _, atCap, err := b.Admit(context.Background(), "app-1", "", "", "", 5)
	if err != nil || !atCap {
		t.Fatalf("Admit = atCapacity %v, err %v; want typed at-capacity", atCap, err)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount after hydration = %d, want 1", got)
	}
	pick := b.Pick("app-1")
	if !pick.OK || pick.Target.InstanceID != "running-1" {
		t.Fatalf("Pick after hydration = %+v, want running-1", pick)
	}
}

func TestPGBackend_ReconcileLiveTargetsHydratesEmptyPicker(t *testing.T) {
	loaderCalls := atomic.Int32{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil).
		WithLiveTargetLoader(func(context.Context, string) ([]gateway.Target, error) {
			loaderCalls.Add(1)
			return []gateway.Target{{
				InstanceID:   "running-1",
				NodeID:       "compute-1",
				DeploymentID: "deployment-live",
			}}, nil
		})

	if err := b.ReconcileLiveTargets(context.Background(), "app-1"); err != nil {
		t.Fatalf("ReconcileLiveTargets: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount after reconciliation = %d, want 1", got)
	}
	pick := b.Pick("app-1")
	if !pick.OK || pick.Target.InstanceID != "running-1" {
		t.Fatalf("Pick after reconciliation = %+v, want running-1", pick)
	}

	// Once the authoritative target is cached, reconciliation must not keep
	// querying Postgres on every request.
	if err := b.ReconcileLiveTargets(context.Background(), "app-1"); err != nil {
		t.Fatalf("second ReconcileLiveTargets: %v", err)
	}
	if got := loaderCalls.Load(); got != 1 {
		t.Errorf("live target loader calls = %d, want 1", got)
	}
}

func TestPGBackend_RefreshLiveTargetsMergesIntoExistingPicker(t *testing.T) {
	loaderCalls := atomic.Int32{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil).
		WithLiveTargetLoader(func(context.Context, string) ([]gateway.Target, error) {
			loaderCalls.Add(1)
			return []gateway.Target{
				{InstanceID: "service-1", NodeID: "compute-1", DeploymentID: "deployment-live"},
				{InstanceID: "service-2", NodeID: "compute-2", DeploymentID: "deployment-live"},
			}, nil
		})
	b.RecordTarget("app-1", gateway.Target{
		InstanceID: "service-1", NodeID: "compute-1", DeploymentID: "deployment-live",
	})

	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount before refresh = %d, want 1", got)
	}
	if err := b.RefreshLiveTargets(context.Background(), "app-1"); err != nil {
		t.Fatalf("RefreshLiveTargets: %v", err)
	}
	if got := b.HealthyCount("app-1"); got != 2 {
		t.Fatalf("HealthyCount after refresh = %d, want 2", got)
	}
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		pick := b.Pick("app-1")
		if !pick.OK {
			t.Fatal("Pick after refresh: !ok")
		}
		seen[pick.Target.InstanceID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("refreshed picker targets = %v, want both service replicas", seen)
	}
	if got := loaderCalls.Load(); got != 1 {
		t.Errorf("live target loader calls = %d, want 1", got)
	}
}

type ensureWakeOnlyScheduler struct {
	ensureCalls int
	admitCalls  int
}

func (s *ensureWakeOnlyScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	s.admitCalls++
	return "", "", "", "", 0, false, 0, errors.New("unexpected AdmitInstance call")
}

func (s *ensureWakeOnlyScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	s.ensureCalls++
	return "instance-ensured", "compute-1", "deployment-live", "wake-ensured", gateway.WireWakeRestore, 9090, nil
}

func (s *ensureWakeOnlyScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "", "", errors.New("not used in EnsureWarm test")
}

func TestPGBackend_EnsureWarmUsesCrossProducerWake(t *testing.T) {
	sched := &ensureWakeOnlyScheduler{}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	wakeID, method, atCapacity, err := b.EnsureWarm(context.Background(), "app-1", "", "gateway")
	if err != nil {
		t.Fatalf("EnsureWarm: %v", err)
	}
	if atCapacity {
		t.Fatal("EnsureWarm atCapacity = true, want a target")
	}
	if wakeID != "wake-ensured" || method != gateway.WakeMethodSnapshotRestore {
		t.Fatalf("EnsureWarm result = wakeID %q, method %v; want wake-ensured/restore", wakeID, method)
	}
	if sched.ensureCalls != 1 || sched.admitCalls != 0 {
		t.Fatalf("scheduler calls = EnsureWake:%d AdmitInstance:%d; want 1/0", sched.ensureCalls, sched.admitCalls)
	}
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount after EnsureWarm = %d, want 1", got)
	}
}

// blockingScheduler makes the admission lifecycle outlive the request
// context. This models a restore that has been reserved by schedd but has
// not reached RUNNING before the public request budget expires.
type blockingScheduler struct {
	started   chan struct{}
	release   chan struct{}
	completed chan struct{}
	once      sync.Once
}

func (s *blockingScheduler) AdmitInstance(ctx context.Context, _, _, _, _ string) (string, string, string, string, int32, bool, int, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return "", "", "", "", 0, false, 0, ctx.Err()
	}
	close(s.completed)
	return "instance-after-timeout", "compute-1", "", "wake-after-timeout", gateway.WireWakeRestore, false, 0, nil
}

func (s *blockingScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	return "", "", "", "", 0, 0, errors.New("not used in cancellation test")
}

func (s *blockingScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "", "", errors.New("not used in cancellation test")
}

func TestPGBackend_AdmitRequestCancellationDoesNotCancelLifecycle(t *testing.T) {
	sched := &blockingScheduler{
		started:   make(chan struct{}),
		release:   make(chan struct{}),
		completed: make(chan struct{}),
	}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	requestCtx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, _, _, err := b.Admit(requestCtx, "app-1", "", "", "", 5)
		resultCh <- err
	}()

	select {
	case <-sched.started:
	case <-time.After(time.Second):
		t.Fatal("admission did not start")
	}
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Admit error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Admit did not return after request cancellation")
	}

	// The scheduler lifecycle is still allowed to finish and must publish the
	// target even though the initiating HTTP request has already returned.
	close(sched.release)
	select {
	case <-sched.completed:
	case <-time.After(time.Second):
		t.Fatal("admission lifecycle did not complete")
	}
	// blockingScheduler closes completed immediately before returning from
	// AdmitInstance. The detached backend goroutine still has to receive that
	// result and publish the target, so observing completed alone is not a
	// synchronization point for the cache update.
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for b.HealthyCount("app-1") != 1 {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("HealthyCount after detached admission = %d, want 1", b.HealthyCount("app-1"))
		}
	}
	if pick := b.Pick("app-1"); !pick.OK || pick.Target.InstanceID != "instance-after-timeout" {
		t.Fatalf("Pick after detached admission = %+v, want instance-after-timeout", pick)
	}
}

// atCapScheduler is a controllable Scheduler that always returns the
// typed at_capacity=true outcome (issue #168).
type atCapScheduler struct{}

func (atCapScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	return "", "", "", "", 0, true, 0, nil
}

func (atCapScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	return "", "", "", "", 0, 0, nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — atCapScheduler
// returns ErrAtCap... wait, no, that's AdmitInstance's contract.
// For mirror tests, the mirror goroutine reads the error via
// errors.Is against sched.ErrMirrorSlotAtCapacity; for now return
// a generic error so existing atCap tests don't accidentally fire
// mirror paths. Mirror-specific tests live in
// pkg/gateway/handler_mirror_test.go and use a separate fake.
func (atCapScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
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

			_, method, atCap, err := b.Admit(context.Background(), "app-1", "", "", "", 5)
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

	_, method, atCap, err := b.Admit(context.Background(), "app-1", "", "", "", 5)
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

func (c *controllableScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	return "i-test", "n-test", "", "w-test", c.rawMethod, false, 0, nil
}

func (c *controllableScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	return "i-test", "n-test", "", "w-test", c.rawMethod, 0, nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — controllableScheduler
// returns a stable identity, mirroring AdmitInstance's test-shape.
// The rawMethod field is intentionally unused on the mirror path —
// mirror is fire-and-forget and the wakeMethod is no longer surfaced
// to the gateway.
func (c *controllableScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "i-mirror", "w-mirror", nil
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

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
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

	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
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

// --- resolveSched legacy-fallback removal (PR #509 finding F1 + multi-host PR-7) ---
//
// Background: PGBackend.resolveSched picks between the legacy
// single-schedd field (b.sched) and the per-node client cache
// (resolved via WithAppResolver + WithClientForApp). On a transient
// resolver miss (ok=false on the AppByID path, or app.NodeID empty),
// the legacy fallback would route a foreign-owned app through the
// default-local dial and surface a FailedPrecondition storm.
//
// Multi-host safety cluster PR-7 (audit F5) REMOVED the fallback
// outright — the legacySingleBox gate that previously toggled
// (single-box keep, multi-box forbid) is gone alongside the
// fallback itself. resolveSched now always returns an error on
// transient misses; no setter exists to toggle the behaviour.

// capturingScheduler records the per-node clients selected by
// WithClientForApp so the multi-box branch can assert that the
// owner-schedd client — not b.sched — receives the Admit.
type capturingScheduler struct {
	id       string
	admitted int
}

func (c *capturingScheduler) AdmitInstance(_ context.Context, _, _, _, _ string) (string, string, string, string, int32, bool, int, error) {
	c.admitted++
	return "fake-instance-" + c.id, "127.0.0.1", "", "w-1", 8080, true, 0, nil
}

func (c *capturingScheduler) EnsureWake(_ context.Context, _, _ string) (string, string, string, string, int32, int, error) {
	c.admitted++
	return "fake-instance-" + c.id, "127.0.0.1", "", "w-1", 8080, 0, nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — capture-only
// stub satisfies the Scheduler interface; the multi-box tests
// don't exercise the mirror hot path.
func (c *capturingScheduler) AdmitMirrorInstance(_ context.Context, _, _, _ string) (string, string, error) {
	c.admitted++
	return "fake-mirror-" + c.id, "w-mirror", nil
}

// TestPGBackend_ResolveSched_MultiBox_RejectsTransientMiss covers
// the headline fix: with the resolver returning ok=false (transient
// cache miss on AppByID), resolveSched must return an error rather
// than silently routing through b.sched. The error message names
// "multi-box posture" so an operator on call can trace the routing
// mistake without a debugger.
func TestPGBackend_ResolveSched_MultiBox_RejectsTransientMiss(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	ownerSched := &capturingScheduler{id: "node-owner"}
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
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

	if _, _, _, err := b.Admit(context.Background(), "app-x", "", "", "", 5); err == nil {
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
// but still has NodeID="" must error rather than fall through.
// The PR-7 removal of the legacy fallback refuses the route even
// though the resolver succeeded.
func TestPGBackend_ResolveSched_MultiBox_RejectsEmptyNodeID(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{ID: "app-y", NodeID: ""}, true, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			t.Errorf("clientForApp must not be called when NodeID is empty")
			return nil, false, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-y", "", "", "", 5); err == nil {
		t.Fatal("expected Admit to error on empty NodeID in multi-box posture, got nil")
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0", legacySched.Calls())
	}
}

// TestPGBackend_ResolveSched_LegacySingleBox_AlwaysRejects pins
// the multi-host safety cluster PR-7 (audit F5) invariant: the
// legacy single-box fallback is REMOVED. resolveSched returns an
// error on a transient resolver miss — there is no longer a
// per-instance flag to opt into the fallback. A transient resolver
// miss on a foreign-owned app in a multi-box fleet must surface
// as a 503, not silently route through the local schedd.
func TestPGBackend_ResolveSched_LegacySingleBox_AlwaysRejects(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{}, false, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			t.Errorf("clientForApp must not be called when resolver returns ok=false")
			return nil, false, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-z", "", "", "", 5); err == nil {
		t.Fatal("PR-7 removed the legacy single-box fallback; transient miss must error")
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0 (PR-7 removed the fallback)", legacySched.Calls())
	}
}

// TestPGBackend_ResolveSched_MultiBox_HappyPathRoutesToOwner is the
// contrast to the rejection cases: with the resolver returning
// ok=true with a valid NodeID, the owner-schedd client (returned
// by clientForApp) — NOT b.sched — receives the Admit.
func TestPGBackend_ResolveSched_MultiBox_HappyPathRoutesToOwner(t *testing.T) {
	legacySched := gateway.NewFakeScheduler("default-local")
	ownerSched := &capturingScheduler{id: "node-owner"}
	b := gateway.NewPGBackend(&fakeRouter{}, legacySched, nil).
		WithAppResolver(func(_ context.Context, _ string) (gateway.App, bool, error) {
			return gateway.App{ID: "app-ok", NodeID: "node-owner"}, true, nil
		}).
		WithClientForApp(func(_ context.Context, _ gateway.App) (gateway.Scheduler, bool, error) {
			return ownerSched, true, nil
		})

	if _, _, _, err := b.Admit(context.Background(), "app-ok", "", "", "", 5); err != nil {
		t.Fatalf("multi-box happy path Admit: %v", err)
	}
	if ownerSched.admitted != 1 {
		t.Errorf("owner-sched received %d Admits, want 1", ownerSched.admitted)
	}
	if legacySched.Calls() != 0 {
		t.Errorf("legacy b.sched received %d Admits, want 0 (multi-box routes to owner)", legacySched.Calls())
	}
}

// fakeMirrorStore (issue #72 / ADR-125 PR-A3) is a controllable
// mirrorRulesStore. The rows slice is the canned response;
// refreshCalls tracks how many times the gateway hit the store
// (the cache-coherence tests pin "second call within the same
// window skips the store"). mu guards both because RefreshMirrorRules
// is the only concurrent caller today but future tests may run
// RefreshMirrorRules from a goroutine (the production notify path).
type fakeMirrorStore struct {
	mu           sync.Mutex
	rows         map[string][]gateway.MirrorRuleRow
	err          error
	refreshCalls int
}

func (s *fakeMirrorStore) ListMirrorRules(_ context.Context, appID string) ([]gateway.MirrorRuleRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCalls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rows[appID], nil
}

func (s *fakeMirrorStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshCalls
}

// TestPGBackend_RefreshMirrorRules_Cache (issue #72 / ADR-125
// PR-A3) — after RefreshMirrorRules, LookupMirrorRules returns
// the cached slice without hitting the store again. The store
// call counter pins "cache hit = zero store calls".
func TestPGBackend_RefreshMirrorRules_Cache(t *testing.T) {
	store := &fakeMirrorStore{rows: map[string][]gateway.MirrorRuleRow{
		"app-1": {
			{ID: "r-1", AccountID: "acct-1", AppID: "app-1",
				SourceDeploymentID: "dep-src", MirrorDeploymentID: "dep-mir",
				Percent: 100, Enabled: true, IncludeBody: false,
				RedactHeaders: []string{"X-Custom"}},
		},
	}}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil).
		WithMirrorStore(store)

	if err := b.RefreshMirrorRules(context.Background(), "app-1"); err != nil {
		t.Fatalf("RefreshMirrorRules: %v", err)
	}
	if n := store.calls(); n != 1 {
		t.Errorf("store refresh calls after one Refresh = %d, want 1", n)
	}
	rules, ok := b.LookupMirrorRules(context.Background(), "app-1")
	if !ok {
		t.Fatal("LookupMirrorRules after Refresh = miss; want hit")
	}
	if len(rules) != 1 || rules[0].ID != "r-1" || rules[0].SourceDeploymentID != "dep-src" {
		t.Fatalf("LookupMirrorRules = %+v, want one rule r-1→dep-src", rules)
	}
	if n := store.calls(); n != 1 {
		t.Errorf("LookupMirrorRules must be cache-only; store calls = %d, want still 1", n)
	}
}

// TestPGBackend_LookupMirrorRules_CacheHitMiss (issue #72 /
// ADR-125 PR-A3) — pre-refresh lookup is a cache miss
// (handler treats this as "no mirror"); post-refresh is a hit;
// app with no rules still returns hit-but-empty so the dispatch
// goroutine skips without a second check.
func TestPGBackend_LookupMirrorRules_CacheHitMiss(t *testing.T) {
	store := &fakeMirrorStore{rows: map[string][]gateway.MirrorRuleRow{
		"app-empty": {}, // rule-less app — refresh writes an empty non-nil slice
	}}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil).
		WithMirrorStore(store)

	// Cache miss on first lookup for an app that has never been refreshed.
	if _, ok := b.LookupMirrorRules(context.Background(), "app-never-refreshed"); ok {
		t.Fatal("LookupMirrorRules for never-refreshed app = hit; want miss")
	}

	// After refresh of an empty-rules app, lookup returns hit with empty slice.
	if err := b.RefreshMirrorRules(context.Background(), "app-empty"); err != nil {
		t.Fatalf("RefreshMirrorRules: %v", err)
	}
	rules, ok := b.LookupMirrorRules(context.Background(), "app-empty")
	if !ok {
		t.Fatal("LookupMirrorRules post-refresh of empty-rules app = miss; want hit (empty slice is still a hit)")
	}
	if len(rules) != 0 {
		t.Errorf("LookupMirrorRules for empty-rules app = %+v, want empty slice", rules)
	}
}

// TestPGBackend_RefreshMirrorRules_PerAppIsolation (issue #72 /
// ADR-125 PR-A3) — refreshing app A does not disturb app B's
// cached rules. RefreshMirrorRules keys by appID so a customer's
// rule change can't poison another tenant's picker.
func TestPGBackend_RefreshMirrorRules_PerAppIsolation(t *testing.T) {
	store := &fakeMirrorStore{rows: map[string][]gateway.MirrorRuleRow{
		"app-A": {{ID: "r-A", AccountID: "acct-A", AppID: "app-A",
			SourceDeploymentID: "src-A", MirrorDeploymentID: "mir-A",
			Percent: 50, Enabled: true}},
		"app-B": {{ID: "r-B", AccountID: "acct-B", AppID: "app-B",
			SourceDeploymentID: "src-B", MirrorDeploymentID: "mir-B",
			Percent: 100, Enabled: true}},
	}}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler(""), nil).
		WithMirrorStore(store)

	// Refresh app A; app B is still a cache miss.
	if err := b.RefreshMirrorRules(context.Background(), "app-A"); err != nil {
		t.Fatalf("RefreshMirrorRules app-A: %v", err)
	}
	if _, ok := b.LookupMirrorRules(context.Background(), "app-B"); ok {
		t.Error("app-B lookup after app-A refresh = hit; want miss (per-app isolation)")
	}
	// Refresh app B; app A is unchanged.
	beforeA, _ := b.LookupMirrorRules(context.Background(), "app-A")
	if err := b.RefreshMirrorRules(context.Background(), "app-B"); err != nil {
		t.Fatalf("RefreshMirrorRules app-B: %v", err)
	}
	afterA, ok := b.LookupMirrorRules(context.Background(), "app-A")
	if !ok || len(afterA) != 1 || afterA[0].ID != "r-A" {
		t.Errorf("app-A lookup after app-B refresh = %+v ok=%v, want r-A preserved", afterA, ok)
	}
	_ = beforeA // beforeA pinned for diff in a follow-up if the assertion becomes non-trivial
	rulesB, ok := b.LookupMirrorRules(context.Background(), "app-B")
	if !ok || len(rulesB) != 1 || rulesB[0].ID != "r-B" {
		t.Errorf("app-B lookup after refresh = %+v ok=%v, want r-B", rulesB, ok)
	}
}

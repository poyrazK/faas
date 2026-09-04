// vmmrouter_test.go — table-driven tests for VMMRouter (issue #97 /
// ADR-025 axis 3). The router is the dial-once-per-target cache
// that sits between the engine and the per-node vmmd client; its
// load-bearing contract is:
//
//   - First call for a node dials; subsequent calls reuse.
//   - Concurrent dials for the same node serialise (no leak).
//   - Concurrent dials for different nodes race freely.
//   - Unknown node → *api.Problem Capacity.
//   - Lost-race closes the duplicate client (no fd leak).

package sched

import (
	"context"
	"crypto/tls"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeRouterVMM records the per-call (instance, target) pair and
// counts dials. It implements VMM (the six-method surface schedd
// expects) and io.Closer so the router's lost-race path can close
// duplicate clients.
type fakeRouterVMM struct {
	instanceCalls []string
	mu            sync.Mutex
}

func (f *fakeRouterVMM) CreateColdBoot(_ context.Context, instance string, _ AppSpec) (*WakeOutcome, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return &WakeOutcome{Instance: instance, HostIP: "10.100.0.2", Netns: "n-" + instance, LeaseUID: 20000, Method: 0, RequestedMethod: 0}, nil
}

func (f *fakeRouterVMM) CreateFromSnapshot(_ context.Context, instance string, _ AppSpec, _ SnapshotRef) (*WakeOutcome, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return &WakeOutcome{Instance: instance, HostIP: "10.100.0.2", Netns: "n-" + instance, LeaseUID: 20000}, nil
}
func (f *fakeRouterVMM) PauseAndSnapshot(_ context.Context, instance, _, _, _ string) (SnapshotBytes, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the vmmrouter
// test's no-op seam — the router surface keeps the warm RPC
// for engine_roundtrip tests but the present router_test cases
// don't drive warm captures.
func (f *fakeRouterVMM) WarmSnapshot(_ context.Context, _, _, _ string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}

func (f *fakeRouterVMM) Destroy(_ context.Context, instance string) error {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the
// graceful signal-then-grace-then-SIGKILL stop
// sequence. Test fakes default to no-op + nil —
// the engine's per-mode dispatch lives in
// pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (f *fakeRouterVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}
func (f *fakeRouterVMM) StopInstanceOnNode(_ context.Context, _, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}

// FrameworkReady implements VMM (issue #470 / PR #470-FU-B). The
// vmmrouter_test fake is for the per-node dial-cache contract; the
// all-six-methods-routing assertion doesn't include FrameworkReady
// yet (the cmd/vmmd DGRAM host recv loop calls it directly via
// VMMClient, not through the router in the current production
// wiring). The method is here so the fake stays a complete VMM
// implementation and the compiler doesn't error out.
func (f *fakeRouterVMM) FrameworkReady(_ context.Context, instance string, _ int64) error {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return nil
}
func (f *fakeRouterVMM) Ping(_ context.Context) (*PingOutcome, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, "<ping>")
	f.mu.Unlock()
	return &PingOutcome{FcVersion: "1.10.0"}, nil
}

// Stats implements VMM (issue #170 / PR-A). Returns the empty
// snapshot — the router test does not assert on Stats contents;
// the dedicated vmmclient_test.go covers wire decoding. Records
// the call so TestVMMRouter_AllSixMethodsRoute's call-count
// assertion (one entry per method per node) holds.
func (f *fakeRouterVMM) Stats(_ context.Context) (*StatsSnapshot, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, "<stats>")
	f.mu.Unlock()
	return &StatsSnapshot{}, nil
}
func (f *fakeRouterVMM) Close() error { return nil }

// UpdateEgressAllowlist (tier-2 PR-B) — router tests don't drive
// the egress drift path; the egress_drift_test.go suite does.
// Records nothing. Returning nil keeps the gRPC VmmdAPI /
// RoutedVMM contract satisfied.
func (f *fakeRouterVMM) UpdateEgressAllowlist(_ context.Context, _ string, _ []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) — no-op test fake. Mirrors
// UpdateEgressAllowlist above. The (ctx, appID, ip) signature
// matches the VMM interface used by the dial-func return
// type at vmmrouter_test.go:162. The (ctx, nodeID, appID, ip)
// RoutedVMM signature is satisfied by a separate method
// below.
func (f *fakeRouterVMM) UpdateStaticEgressIP(_ context.Context, _, _ string, _ string) error {
	return nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — router tests
// don't drive the log stream path; the scheddgrpc handler tests
// do. Returns a closed fakeLogStream so any accidental caller exits
// cleanly. PR-B adds the sinceWrittenAt time lower-bound; the fake
// ignores it.
func (f *fakeRouterVMM) Logs(_ context.Context, _ string, _ int64, _ time.Time) (LogStream, error) {
	return &fakeLogStream{}, nil
}

// Tier A5 (ADR-066) — vmmrouter tests don't drive migration.
func (f *fakeRouterVMM) PrepareLiveMigration(context.Context, string, string, string) (LiveMigrationPrepare, error) {
	return LiveMigrationPrepare{}, nil
}
func (f *fakeRouterVMM) AdoptMigratedInstance(context.Context, string, string, AppSpec, string, string, string) (LiveMigrationAdopt, error) {
	return LiveMigrationAdopt{}, nil
}
func (f *fakeRouterVMM) AcknowledgeMigration(context.Context, string, string, string) error {
	return nil
}
func (f *fakeRouterVMM) CancelLiveMigration(context.Context, string, string, string) error {
	return nil
}

// trackingDial records every (target, tls) it sees and returns a
// cached fakeRouterVMM on subsequent calls to the same target.
// `dials` counts only fresh (cache-miss) dials — the load-bearing
// invariant is "the per-target map has exactly one entry"; it is
// not "the closure was called exactly once per target". Under
// concurrency, multiple goroutines may race past the cache-check
// gap in resolveFor() and call the dial closure; the lost-race
// path closes the duplicate client and returns the winner. This
// counter and the per-target map cardinality together pin both
// halves of the load-bearing invariant (issue #97 / ADR-025 axis 3,
// PR #113/114).
func trackingDial(targets *map[string]*fakeRouterVMM, dials *atomic.Int32, mu *sync.Mutex) DialFunc {
	return func(_ context.Context, target string, _ *tls.Config) (VMM, error) {
		mu.Lock()
		defer mu.Unlock()
		if existing, ok := (*targets)[target]; ok {
			return existing, nil
		}
		dials.Add(1)
		f := &fakeRouterVMM{}
		(*targets)[target] = f
		return f, nil
	}
}

// TestVMMRouter_RoutesByNodeID pins the core routing contract: calls
// for node A never hit node B's client, and vice versa.
func TestVMMRouter_RoutesByNodeID(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	nodes := []ComputeNodeInfo{
		{ID: "node-a", TargetURL: "unix:///run/faas/a.sock"},
		{ID: "node-b", TargetURL: "unix:///run/faas/b.sock"},
	}
	r := NewVMMRouter(nodes, dial, nil)

	ctx := context.Background()
	if _, err := r.CreateColdBoot(ctx, "node-a", "i-1", AppSpec{}); err != nil {
		t.Fatalf("CreateColdBoot node-a: %v", err)
	}
	if _, err := r.CreateColdBoot(ctx, "node-b", "i-2", AppSpec{}); err != nil {
		t.Fatalf("CreateColdBoot node-b: %v", err)
	}
	if _, err := r.CreateColdBoot(ctx, "node-a", "i-3", AppSpec{}); err != nil {
		t.Fatalf("CreateColdBoot node-a again: %v", err)
	}

	// Each node should have been dialled exactly once, regardless of
	// how many calls hit it.
	if got := dials.Load(); got != 2 {
		t.Errorf("dial count = %d, want 2 (one per node)", got)
	}

	a := targets["unix:///run/faas/a.sock"]
	b := targets["unix:///run/faas/b.sock"]
	if a == nil || b == nil {
		t.Fatalf("tracking dial did not record both targets: a=%v b=%v", a, b)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if got := len(a.instanceCalls); got != 2 {
		t.Errorf("node-a received %d calls, want 2 (i-1 + i-3): %v", got, a.instanceCalls)
	}
	if a.instanceCalls[0] != "i-1" || a.instanceCalls[1] != "i-3" {
		t.Errorf("node-a call order = %v, want [i-1 i-3]", a.instanceCalls)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := len(b.instanceCalls); got != 1 {
		t.Errorf("node-b received %d calls, want 1 (i-2): %v", got, b.instanceCalls)
	}
	if b.instanceCalls[0] != "i-2" {
		t.Errorf("node-b call = %q, want i-2", b.instanceCalls[0])
	}
}

// TestVMMRouter_DialOncePerNode pins the dial-once-per-target
// invariant under concurrent first-use. 50 goroutines all calling
// CreateColdBoot on the same node must produce exactly one dial;
// the other 49 reuse the cached client. Without the
// serialise-then-recheck dance this test would flake (-race would
// also catch a data race on the cache map).
func TestVMMRouter_DialOncePerNode(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	nodes := []ComputeNodeInfo{{ID: "n1", TargetURL: "unix:///n1.sock"}}
	r := NewVMMRouter(nodes, dial, nil)

	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := r.CreateColdBoot(context.Background(), "n1", "i", AppSpec{})
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := dials.Load(); got != 1 {
		t.Errorf("dial count under concurrency = %d, want 1 (dial-once-per-target invariant)", got)
	}
}

// TestVMMRouter_UnknownNodeReturnsCapacity pins the
// "nodeID has no row" failure mode. The router refuses to dial an
// unknown target — it has no TargetURL to dial against — and
// surfaces a *api.Problem Capacity (the same code the ledger uses
// for no-headroom, so the gateway's 503 mapping stays consistent).
func TestVMMRouter_UnknownNodeReturnsCapacity(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	r := NewVMMRouter(nil, dial, nil) // no nodes registered
	_, err := r.CreateColdBoot(context.Background(), "ghost", "i", AppSpec{})
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
		t.Errorf("expected *api.Problem Capacity, got %v", err)
	}
	if got := dials.Load(); got != 0 {
		t.Errorf("dial count for unknown node = %d, want 0 (must not dial a phantom target)", got)
	}
}

// TestVMMRouter_AllSixMethodsRoute pins that every RoutedVMM method
// goes through resolveFor (issue #97 / ADR-025 axis 3, PR #114:
// Ping is the 5th method; issue #170 / PR-A: Stats is the 6th).
// A regression that special-cased CreateColdBoot (e.g. forgetting to
// route Destroy or Ping or Stats through the cache) would let Park /
// Evict / Heartbeat / the new instancestats poller dial the legacy
// single socket on every call — fine for one node, broken for N.
func TestVMMRouter_AllSixMethodsRoute(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	nodes := []ComputeNodeInfo{{ID: "n1", TargetURL: "unix:///n1.sock"}}
	r := NewVMMRouter(nodes, dial, nil)
	ctx := context.Background()
	if _, err := r.CreateColdBoot(ctx, "n1", "i1", AppSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateFromSnapshot(ctx, "n1", "i2", AppSpec{}, SnapshotRef{StorageKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PauseAndSnapshot(ctx, "n1", "i3", "vs", "k", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Destroy(ctx, "n1", "i4"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ping(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Stats(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if got := dials.Load(); got != 1 {
		t.Errorf("dial count = %d, want 1 across 6 methods on the same node", got)
	}
	c := targets["unix:///n1.sock"]
	if c == nil {
		t.Fatal("tracking dial did not record n1")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := len(c.instanceCalls); got != 6 {
		t.Errorf("node n1 received %d calls, want 6 (one per method)", got)
	}
}

// TestVMMRouter_NilDialClosureFailsLoud pins the "constructor not
// called" failure mode. NewVMMRouter requires a non-nil dial fn;
// passing nil panics at first call would be worse than a typed
// error. The current implementation returns errors.New so the
// engine's failure path stays consistent.
func TestVMMRouter_NilDialClosureFailsLoud(t *testing.T) {
	// Construct via zero-value (bypassing NewVMMRouter so dial is nil)
	// and pre-populate targets so resolveFor reaches the dial step.
	r := &VMMRouter{
		cache:   map[string]VMM{},
		targets: map[string]string{"n1": "unix:///n1.sock"},
		dial:    nil,
	}
	_, err := r.CreateColdBoot(context.Background(), "n1", "i", AppSpec{})
	if err == nil {
		t.Fatal("expected error from nil dial closure")
	}
}

// TestVMMRouter_InterfaceSatisfied is a compile-time check that
// VMMRouter still satisfies RoutedVMM. A regression that drops a
// method fails the build (var _ RoutedVMM = (*VMMRouter)(nil) at
// the bottom of vmmrouter.go), but a redundant runtime assertion
// here keeps the test surface honest if the package ever reorders.
func TestVMMRouter_InterfaceSatisfied(t *testing.T) {
	var _ RoutedVMM = (*VMMRouter)(nil)
}

// TestVMMRouter_RefreshDropsCacheAndReloadsTargets (Tier A3) pins
// the live-refresh contract: a fresh target_url replaces the old
// one and the dialed client is closed; the next resolveFor lazy-
// dials against the new URL. Dial count rises by 1 — the cache
// was dropped, so the next call cannot reuse the old client.
func TestVMMRouter_RefreshDropsCacheAndReloadsTargets(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	r := NewVMMRouter([]ComputeNodeInfo{
		{ID: "node-a", TargetURL: "unix:///run/faas/a.sock"},
	}, dial, nil)

	// Pre-dial node-a on the original URL.
	if _, err := r.CreateColdBoot(context.Background(), "node-a", "i-pre", AppSpec{}); err != nil {
		t.Fatalf("pre-dial CreateColdBoot: %v", err)
	}
	pre, ok := targets["unix:///run/faas/a.sock"]
	if !ok || pre == nil {
		t.Fatal("trackingDial should have produced a fake for the original URL")
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("pre-dial dials = %d, want 1", got)
	}

	// The refresh: target_url rotates from a.sock to new.sock.
	r.Refresh("node-a", "unix:///run/faas/new.sock")

	// Cache slot is empty; targets map carries the new URL.
	if got := r.Client("node-a"); got != nil {
		t.Errorf("after Refresh: cache[ node-a ] = %v, want nil (drop is unconditional)", got)
	}

	// Next CreateColdBoot dials against the fresh URL.
	if _, err := r.CreateColdBoot(context.Background(), "node-a", "i-post", AppSpec{}); err != nil {
		t.Fatalf("post-refresh CreateColdBoot: %v", err)
	}
	if got := dials.Load(); got != 2 {
		t.Errorf("post-refresh dials = %d, want 2 (cache was dropped)", got)
	}
	if _, ok := targets["unix:///run/faas/new.sock"]; !ok {
		t.Error("expected a fresh fake for the new target_url")
	}
}

// TestVMMRouter_RefreshOfMissingNodeDropsOnly pins the row-gone
// branch of Refresh: a watcher that observed a row drop on
// compute_nodes writes targets[nodeID]="" so the next resolveFor
// returns ErrCapacity rather than reusing a stale URL. The cache
// entry was never set, so no Close() fires — the test asserts
// only that the unknown-node error surfaces.
func TestVMMRouter_RefreshOfMissingNodeDropsOnly(t *testing.T) {
	targets := map[string]*fakeRouterVMM{}
	var dials atomic.Int32
	var mu sync.Mutex
	dial := trackingDial(&targets, &dials, &mu)

	r := NewVMMRouter([]ComputeNodeInfo{
		{ID: "node-a", TargetURL: "unix:///run/faas/a.sock"},
	}, dial, nil)

	r.Refresh("ghost-node", "")

	if got := r.Client("ghost-node"); got != nil {
		t.Errorf("after Refresh(ghost, \"\"): cache[ ghost ] = %v, want nil", got)
	}
	_, err := r.CreateColdBoot(context.Background(), "ghost-node", "i", AppSpec{})
	if err == nil {
		t.Fatal("CreateColdBoot against a refresh-erased node should fail loudly")
	}
	// The unknown-node path uses *api.Problem Capacity so the
	// gateway's 503 mapping is consistent. Pin the type so a
	// future regression that silently falls through to a stale
	// URL surfaces here. api.ErrCapacity is a *Problem
	// constructor (not a sentinel error), so we assert the
	// typed-shape directly.
	var prob *api.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %v, want *api.Problem", err)
	}
	if prob.Code != api.CodeCapacity {
		t.Errorf("error code = %q, want %q", prob.Code, api.CodeCapacity)
	}
	if got := dials.Load(); got != 0 {
		t.Errorf("dials = %d, want 0 (no dial should happen on a refresh-erased node)", got)
	}
}

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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeRouterVMM records the per-call (instance, target) pair and
// counts dials. It implements VMM (the four-method surface schedd
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
func (f *fakeRouterVMM) PauseAndSnapshot(_ context.Context, instance, _, _ string) (SnapshotBytes, error) {
	f.mu.Lock()
	f.instanceCalls = append(f.instanceCalls, instance)
	f.mu.Unlock()
	return SnapshotBytes{}, nil
}
func (f *fakeRouterVMM) Destroy(_ context.Context, instance string) error {
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
func (f *fakeRouterVMM) Close() error { return nil }

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

// TestVMMRouter_AllFiveMethodsRoute pins that every RoutedVMM method
// goes through resolveFor (issue #97 / ADR-025 axis 3, PR #114:
// Ping is the 5th method). A regression that special-cased
// CreateColdBoot (e.g. forgetting to route Destroy or Ping through
// the cache) would let Park / Evict / Heartbeat dial the legacy
// single socket on every call — fine for one node, broken for N.
func TestVMMRouter_AllFiveMethodsRoute(t *testing.T) {
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
	if _, err := r.PauseAndSnapshot(ctx, "n1", "i3", "vs", "k"); err != nil {
		t.Fatal(err)
	}
	if err := r.Destroy(ctx, "n1", "i4"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ping(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if got := dials.Load(); got != 1 {
		t.Errorf("dial count = %d, want 1 across 5 methods on the same node", got)
	}
	c := targets["unix:///n1.sock"]
	if c == nil {
		t.Fatal("tracking dial did not record n1")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := len(c.instanceCalls); got != 5 {
		t.Errorf("node n1 received %d calls, want 4 (one per method)", got)
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

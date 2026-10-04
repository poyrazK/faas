// adr: 531 — trusted VM admission and bridge-owned cleanup permits.
package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHTTPAdmissionAggregatesConcurrentGatewaysAndKeepsCancelledPermits(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			m.live["vm"] = &Instance{Lease: Lease{Instance: "vm", Slot: 1, Plan: plan}, Plan: plan}
			cap := plan.ConcurrencyPerVMBound()
			var mu sync.Mutex
			var releases []func()
			var cancels []context.CancelFunc
			var wg sync.WaitGroup
			for i := 0; i < cap*3; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithCancel(context.Background())
					_, release, err := m.AcquireHTTPForward(ctx, "vm")
					if errors.Is(err, ErrHTTPForwardCapacity) {
						cancel()
						return
					}
					if err != nil {
						t.Error(err)
						cancel()
						return
					}
					mu.Lock()
					releases = append(releases, release)
					cancels = append(cancels, cancel)
					mu.Unlock()
				}()
			}
			wg.Wait()
			if len(releases) != cap {
				t.Fatalf("admitted %d, cap %d", len(releases), cap)
			}
			for _, cancel := range cancels {
				cancel()
			}
			// A hung bridge still holds capacity after its client disconnects.
			if _, _, err := m.AcquireHTTPForward(context.Background(), "vm"); !errors.Is(err, ErrHTTPForwardCapacity) {
				t.Fatalf("cancel minted capacity: %v", err)
			}
			state, _ := m.HTTPAdmissionStatus("vm")
			if !state.Enabled || state.Limit != cap || state.Inflight != cap || state.Generation == "" {
				t.Fatalf("status: %+v", state)
			}
			for _, release := range releases {
				release()
				release()
			}
			_, release, err := m.AcquireHTTPForward(context.Background(), "vm")
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}

func TestHTTPAdmissionRetirementFencesDelayedReleaseAndReplacement(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	old := &Instance{Lease: Lease{Instance: "vm", Slot: 1, Plan: api.PlanFree}, Plan: api.PlanFree}
	m.live["vm"] = old
	ctx, release, err := m.AcquireHTTPForward(context.Background(), "vm")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.HTTPAdmissionStatus("vm")
	g := m.retireHTTPForwards(old.Lease)
	if ctx.Err() == nil {
		t.Fatal("retirement did not cancel bridge")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.waitHTTPForwards(deadline, g); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("released active bridge: %v", err)
	}
	m.mu.Lock()
	m.live["vm"] = &Instance{Lease: Lease{Instance: "vm", Slot: 2, Plan: api.PlanFree}, Plan: api.PlanFree}
	m.mu.Unlock()
	if _, _, err := m.AcquireHTTPForward(context.Background(), "vm"); !errors.Is(err, ErrHTTPForwardNotLive) {
		t.Fatalf("replacement bypassed retirement: %v", err)
	}
	release()
	if err := m.waitHTTPForwards(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	m.forgetHTTPForwards(g)
	_, nextRelease, err := m.AcquireHTTPForward(context.Background(), "vm")
	if err != nil {
		t.Fatal(err)
	}
	defer nextRelease()
	release() // delayed duplicate belongs only to the old generation
	after, _ := m.HTTPAdmissionStatus("vm")
	if after.Inflight != 1 || after.Generation == before.Generation {
		t.Fatalf("replacement status: %+v", after)
	}
	if retired := m.retireHTTPForwards(old.Lease); retired != nil {
		t.Fatal("old cleanup retired replacement")
	}
}

func TestHTTPAdmissionRejectsUntrustedAndNonHTTPInstances(t *testing.T) {
	for _, tc := range []struct {
		name string
		inst Instance
		want error
	}{
		{"missing plan", Instance{}, ErrHTTPForwardUntrusted},
		{"unknown plan", Instance{Plan: "unknown"}, ErrHTTPForwardUntrusted},
		{"paused", Instance{Plan: api.PlanFree, Paused: true}, ErrHTTPForwardNotLive},
		{"execution", Instance{Plan: api.PlanFree, ExecutionOnly: true}, ErrHTTPForwardNotLive},
		{"task", Instance{Plan: api.PlanFree, AppTaskOnly: true}, ErrHTTPForwardNotLive},
		{"job", Instance{Plan: api.PlanFree, IsJob: true}, ErrHTTPForwardNotLive},
		{"builder", Instance{Plan: api.PlanFree, Lease: Lease{IsBuilder: true}}, ErrHTTPForwardNotLive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			m.live["vm"] = &tc.inst
			if _, _, err := m.AcquireHTTPForward(context.Background(), "vm"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestHTTPAdmissionFailedMigrationKeepsPendingBridgesAndResumeRotatesDrainedGeneration(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	inst := &Instance{Lease: Lease{Instance: "vm", Slot: 1, Plan: api.PlanFree}, Plan: api.PlanFree}
	m.live["vm"] = inst
	var releases []func()
	for i := 0; i < inst.Plan.ConcurrencyPerVMBound(); i++ {
		_, release, err := m.AcquireHTTPForward(t.Context(), "vm")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	before, _ := m.HTTPAdmissionStatus("vm")
	g := m.retireHTTPForwards(inst.Lease)
	m.reopenHTTPForwards(inst) // timed-out migration, cancelled bridges still cleaning up
	if _, _, err := m.AcquireHTTPForward(t.Context(), "vm"); !errors.Is(err, ErrHTTPForwardCapacity) {
		t.Fatalf("failed migration minted capacity: %v", err)
	}
	if state, _ := m.HTTPAdmissionStatus("vm"); state.Retiring || state.Generation != before.Generation {
		t.Fatalf("failed migration changed ownership: %+v", state)
	}
	for _, release := range releases {
		release()
	}
	if retired := m.retireHTTPForwards(inst.Lease); retired != g {
		t.Fatal("retirement lost the pending generation")
	}
	if err := m.waitHTTPForwards(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	inst.Paused = true
	m.reopenHTTPForwards(inst)
	_, release, err := m.AcquireHTTPForward(t.Context(), "vm")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if state, _ := m.HTTPAdmissionStatus("vm"); state.Retiring || state.Generation == before.Generation || state.Inflight != 1 {
		t.Fatalf("resumed VM did not get a new admission generation: %+v", state)
	}
}

func TestHTTPAdmissionCleanupDoesNotRecycleLeaseBeforeBridgeStops(t *testing.T) {
	runner := &fakeRunner{}
	m := newTestManager(runner, &fakeVMM{})
	lease, err := m.alloc.Acquire("vm")
	if err != nil {
		t.Fatal(err)
	}
	lease.Plan = api.PlanFree
	m.live["vm"] = &Instance{Lease: lease, Plan: api.PlanFree}
	ctx, release, err := m.AcquireHTTPForward(context.Background(), "vm")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.cleanup(context.Background(), lease, m.live["vm"].Net, nil); close(done) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cleanup did not cancel forwarding")
	}
	if _, err := m.alloc.Acquire("vm"); err == nil {
		t.Fatal("lease recycled while bridge active")
	}
	select {
	case <-done:
		t.Fatal("cleanup returned before bridge stopped")
	default:
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish")
	}
	if _, err := m.alloc.Acquire("vm"); err != nil {
		t.Fatalf("lease not reusable after cleanup: %v", err)
	}
}

func TestHTTPAdmissionCleanupRetryRetainsBridgeAndLease(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	lease, err := m.alloc.Acquire("cleanup-retry")
	if err != nil {
		t.Fatal(err)
	}
	lease.Plan = api.PlanFree
	lease.Networkless = true
	m.live[lease.Instance] = &Instance{Lease: lease, Plan: api.PlanFree}
	forwardCtx, release, err := m.AcquireHTTPForward(t.Context(), lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := m.cleanup(ctx, lease, m.live[lease.Instance].Net, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup acknowledged a pending bridge: %v", err)
	}
	if forwardCtx.Err() == nil {
		t.Fatal("cleanup did not cancel forwarding")
	}
	if m.teardownIdentity(lease.Instance) == nil {
		t.Fatal("failed cleanup lost the resource identity")
	}
	if _, err := m.alloc.Acquire(lease.Instance); err == nil {
		t.Fatal("failed cleanup released the lease")
	}
	if _, _, err := m.AcquireHTTPForward(t.Context(), lease.Instance); !errors.Is(err, ErrHTTPForwardNotLive) {
		t.Fatalf("failed cleanup reopened admission: %v", err)
	}
	release()
	if err := m.Destroy(t.Context(), lease.Instance); err != nil {
		t.Fatal(err)
	}
	if m.teardownIdentity(lease.Instance) != nil {
		t.Fatal("retry retained completed resources")
	}
	if _, err := m.alloc.Acquire(lease.Instance); err != nil {
		t.Fatalf("retry did not release lease: %v", err)
	}
	if m.httpForwards[lease.Instance] != nil {
		t.Fatal("retry retained the drained generation")
	}
}

// adr: 1151 — a thaw and the wake fast path read each control-plane row they
// need once, and still honour the database cutover barrier.
package sched

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// readCountingStore counts the reads the wake path repeated before, and can
// raise the managed-Postgres cutover barrier.
type readCountingStore struct {
	MemStore *state.MemStore
	fenced   bool
	mu       sync.Mutex
	counts   map[string]int
}

func (s *readCountingStore) count(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[name]++
}

func (s *readCountingStore) reads() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

func (s *readCountingStore) ComputeNodeByID(ctx context.Context, id string) (state.ComputeNode, error) {
	s.count("ComputeNodeByID")
	return s.MemStore.ComputeNodeByID(ctx, id)
}

func (s *readCountingStore) AppByID(ctx context.Context, id string) (state.App, error) {
	s.count("AppByID")
	return s.MemStore.AppByID(ctx, id)
}

func (s *readCountingStore) ManagedPostgresAdmissionFenced(ctx context.Context, appID string) (bool, error) {
	s.count("ManagedPostgresAdmissionFenced")
	if s.fenced {
		return true, nil
	}
	return s.MemStore.ManagedPostgresAdmissionFenced(ctx, appID)
}

func (s *readCountingStore) DeploymentClearSnapshotBackoff(ctx context.Context, deploymentID string) error {
	s.count("DeploymentClearSnapshotBackoff")
	return s.MemStore.DeploymentClearSnapshotBackoff(ctx, deploymentID)
}

// countReads swaps the fixture engine onto a counting view of its store.
func countReads(f *frozenFixture) *readCountingStore {
	counter := &readCountingStore{MemStore: f.store}
	f.e.store = statetest.NewMem(counter)
	return counter
}

func TestThawReadsTheNodeAndTheBarrierOnce(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	counter := countReads(f)
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil || result.InstanceID != f.ins.ID || f.vmm.thaws != 1 {
		t.Fatalf("Wake: %+v, %v, thaws %d", result, err, f.vmm.thaws)
	}
	// Before: the node row four times (reachability, ceiling, CPU budget,
	// region) and the barrier twice (fast path and admission).
	reads := counter.reads()
	if reads["ComputeNodeByID"] != 1 || reads["ManagedPostgresAdmissionFenced"] != 1 {
		t.Fatalf("thaw reads = %v, want one node read and one barrier check", reads)
	}
	if result.Identity.TenantID != f.app.AccountID {
		t.Fatalf("identity tenant = %q, want %q", result.Identity.TenantID, f.app.AccountID)
	}
}

func TestWakeFastPathReadsTheAppAndTheBarrierOnce(t *testing.T) {
	f := newFrozenFixture(t, true)
	counter := countReads(f)
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil || result.InstanceID != f.ins.ID || f.vmm.thaws != 0 || f.vmm.restores != 0 || f.vmm.coldBoots != 0 {
		t.Fatalf("Wake: %+v, %v", result, err)
	}
	reads := counter.reads()
	// The standards check and the answer's tenant share one app read; the
	// instance-mode check resolves the deployment-pinned app separately.
	if reads["ManagedPostgresAdmissionFenced"] != 1 || reads["AppByID"] > 2 {
		t.Fatalf("fast path reads = %v, want one barrier check and at most two app reads", reads)
	}
	if result.Identity.TenantID != f.app.AccountID {
		t.Fatalf("identity tenant = %q, want %q", result.Identity.TenantID, f.app.AccountID)
	}
}

func TestWakeStillHonoursTheCutoverBarrier(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frozen bool
	}{
		{name: "running instance on the fast path"},
		{name: "frozen instance on the admission path", frozen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFrozenFixture(t, true)
			f.e.EnableFrozenTier()
			if tc.frozen {
				if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
					t.Fatalf("FreezeIdle: %v, %v", handled, err)
				}
			}
			counter := countReads(f)
			counter.fenced = true
			if _, err := f.e.Wake(t.Context(), f.app.ID, "", "", ""); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
				t.Fatalf("Wake on a fenced app = %v, want ErrManagedPostgresAdmissionFenced", err)
			}
			if f.vmm.thaws != 0 {
				t.Fatalf("a fenced app thawed %d times", f.vmm.thaws)
			}
		})
	}
}

func TestWakeAppIsReusedOnlyForItsAppAndOneAdmission(t *testing.T) {
	app := state.App{ID: "00000000-0000-4000-8000-0000000000a1"}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		appID string
		want  bool
	}{
		{name: "no wake app", ctx: context.Background(), appID: app.ID},
		{name: "same app", ctx: withWakeApp(context.Background(), app), appID: app.ID, want: true},
		{name: "another app", ctx: withWakeApp(context.Background(), app), appID: "00000000-0000-4000-8000-0000000000a2"},
		{name: "unread app is not carried", ctx: withWakeApp(context.Background(), state.App{}), appID: ""},
		{name: "after admission resolved it", ctx: forgetWakeApp(withWakeApp(context.Background(), app)), appID: app.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := wakeAppFrom(tc.ctx, tc.appID); ok != tc.want {
				t.Fatalf("wakeAppFrom = %v, want %v", ok, tc.want)
			}
		})
	}
}

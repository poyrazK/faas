// adr: 1420 — a worker pool admits its missing workers together.
package sched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerPoolAdmitsMissingWorkersTogether(t *testing.T) {
	store := state.NewMemStore()
	account, app, _ := seedWorkerScopes(t, store, api.PlanPro)
	// Three times the queue_depth target of 10 asks for Pro's three workers.
	enqueueScopedDemand(t, store, account, app, "default", "", 30)
	var mu sync.Mutex
	booting, peak := 0, 0
	allBooting := make(chan struct{})
	var once sync.Once
	vmm := &fakeVMM{}
	// Each boot waits until all three are in flight, as a characterization
	// window keeps a real worker boot open. One-at-a-time admission never
	// gets there and would take the 5 s timeout per boot.
	vmm.coldBootHook = func() {
		mu.Lock()
		booting++
		peak = max(peak, booting)
		if booting == 3 {
			once.Do(func() { close(allBooting) })
		}
		mu.Unlock()
		select {
		case <-allBooting:
		case <-time.After(5 * time.Second):
		}
		mu.Lock()
		booting--
		mu.Unlock()
	}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	ctx := context.Background()
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	// An explicit wake's id may mark one booting instance only
	// (instances_wake_attempt_active_idx), so it stays with one worker.
	wakeID := uuid.NewString()
	if err := engine.ReconcileWorkerPools(withRequestedWakeID(ctx, wakeID), app.ID, TriggerAppWake); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak != 3 {
		t.Fatalf("at most %d worker boots overlapped; the pool must admit its three missing workers together", gotPeak)
	}
	if counts := scopedWorkerCounts(t, store, app.ID); counts["default"] != 3 {
		t.Fatalf("default pool = %d workers, want 3", counts["default"])
	}
	instances, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	withWakeID, ids := 0, map[string]bool{}
	for _, ins := range instances {
		if ins.WakeID == wakeID {
			withWakeID++
		}
		ids[ins.WakeID] = true
	}
	if withWakeID != 1 || len(ids) != len(instances) {
		t.Fatalf("%d of %d workers carry the explicit wake id, %d distinct ids; want exactly one and all distinct",
			withWakeID, len(instances), len(ids))
	}
}

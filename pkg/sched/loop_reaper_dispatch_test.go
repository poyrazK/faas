// adr: 191

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestReaperTickDoesNotBlockSchedulerLoop — runReaper parked sequentially on
// the loop's select goroutine. On production-us a snapshot park took up to
// 35 s (p90 22.75 s), so every wake notification on the node waited behind
// the tick. The tick now runs as one coalesced work-pool task: dispatch
// returns while the park is still capturing, a second tick coalesces into the
// running one, and the park still completes.
func TestReaperTickDoesNotBlockSchedulerLoop(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	vmm.mu.Lock()
	vmm.sleepFor = 500 * time.Millisecond
	vmm.mu.Unlock()

	loop := NewLoop(nil, engine, testLog())
	started := time.Now()
	loop.dispatchReaper(context.Background())
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("dispatchReaper blocked the loop for %s behind a slow park", elapsed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
		if ins.State == string(state.StateSnapshotting) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("park never started; state = %q", ins.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := loop.workPool().submit(workReaper, "tick", func() { t.Error("second reaper tick ran beside the first") }); got != "coalesced" {
		t.Fatalf("second tick outcome = %q, want coalesced", got)
	}
	loop.workPool().drain()
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateParked) {
		t.Fatalf("state after the reaper task = %q, want parked", ins.State)
	}
}

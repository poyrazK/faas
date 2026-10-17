package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// spec: §6
// H10-136: a worker whose delayed replacement was pending when schedd
// restarted had no event left to bring it back. The startup pass admits a
// worker for an active worker app with none live, and leaves request apps and
// evicted worker apps alone.
func TestWorkerStartupReconcileReplacesAFailedWorker(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		park      bool
		wantBoots int
	}{
		{name: "active worker app with only a failed worker", mode: api.ExecutionModeWorker, wantBoots: 1},
		{name: "request app", mode: api.ExecutionModeRequest},
		{name: "evicted worker app", mode: api.ExecutionModeWorker, park: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 4)
			app = configureExecutionMode(t, store, app, dep, tt.mode, state.DeployLive)
			if tt.park {
				if ok, err := store.CompareAndSetAppStatus(ctx, app.ID, state.AppActive, state.AppEvictedCold); err != nil || !ok {
					t.Fatalf("CompareAndSetAppStatus = %v, %v", ok, err)
				}
			}
			if tt.mode == api.ExecutionModeWorker {
				if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateFailed), 256, state.DefaultLocalNodeName, "wake-failed", string(state.InstanceModeWorker)); err != nil {
					t.Fatalf("CreateInstanceWithMode: %v", err)
				}
			}
			vmm := &fakeVMM{}
			loop := NewLoop(nil, newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0"), testLog())

			loop.runWorkerStartupReconcile(ctx)

			if vmm.coldBoots+vmm.restores != tt.wantBoots {
				t.Fatalf("boots = %d cold + %d restore, want %d", vmm.coldBoots, vmm.restores, tt.wantBoots)
			}
		})
	}
}

package databaseswitch_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExpiredDatabaseSwitchWorkerCannotPromoteAfterAbort(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = "ready"
	h.runUntil(state.DatabaseSwitchDraining, 10)
	h.completeDrain()
	h.runUntil(state.DatabaseSwitchSwitching, 3)

	staleWorker := *h.worker
	staleWorker.ReleaseGuard = func(ctx context.Context, old state.DatabaseSwitch) (string, error) {
		// The first worker pauses after loading its release facts. Another APID
		// takes its expired lease and aborts the switch on a new release refusal.
		h.clock = h.clock.Add(api.DatabaseSwitchLease + time.Second)
		replacement := *h.worker
		replacement.ReleaseGuard = func(context.Context, state.DatabaseSwitch) (string, error) { return "new release refusal", nil }
		for range 4 {
			h.clock = h.clock.Add(10 * time.Second)
			replacement.Tick(ctx)
		}
		finished, err := h.store.DatabaseSwitch(ctx, h.sw.AccountID, h.sw.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished.Phase != state.DatabaseSwitchFailed || h.cutovers.fenced {
			t.Fatalf("replacement did not abort: %+v fence=%v", finished, h.cutovers.fenced)
		}
		if h.releases.live["default"] != old.FromDeploymentID {
			t.Fatal("abort control changed the source release")
		}
		return "", nil
	}
	h.clock = h.clock.Add(10 * time.Second)
	due, err := h.store.ClaimDueDatabaseSwitches(t.Context(), "stale-worker", h.clock, h.clock.Add(api.DatabaseSwitchLease), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("claim: %+v %v", due, err)
	}
	staleWorker.Advance(t.Context(), due[0])
	final, err := h.store.DatabaseSwitch(t.Context(), h.sw.AccountID, h.sw.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Phase != state.DatabaseSwitchFailed || h.releases.live["default"] != final.FromDeploymentID || h.cutovers.fenced {
		t.Fatalf("expired worker promoted rollback after replacement aborted: phase=%s target_live=%s fence=%v", final.Phase, final.TargetDeploymentID, h.cutovers.fenced)
	}
}

package sched_test

// adr: 343 — a request wake moves a parked app back to active.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway admission for an app parked through POST /v1/apps/{slug}/park
// claims the parked -> active lifecycle. Left evicted_cold, the reaper's
// must-park sweep destroyed the woken instance on its next tick, including a
// boot still in its cold-boot fallback (n2 TestSourceDeployWakeMetal).
func TestAdmitInstanceActivatesParkedApp(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 1)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	res, err := e.AdmitInstance(context.Background(), app.ID, "", "", sched.TriggerGateway)
	if err != nil || res.AtCapacity || res.InstanceID == "" {
		t.Fatalf("AdmitInstance on a parked app = %+v, %v", res, err)
	}
	current, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != state.AppActive {
		t.Fatalf("app status = %q, want active", current.Status)
	}

	// The reaper's must-park reconcile now leaves the woken instance alone.
	acted, err := e.ParkApp(context.Background(), app.ID)
	if err != nil || acted != 0 {
		t.Fatalf("ParkApp after wake = %d, %v; want 0 instances touched", acted, err)
	}
	ins, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State(ins.State) != state.StateRunning {
		t.Fatalf("woken instance state = %q, want running", ins.State)
	}
}

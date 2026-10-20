package sched_test

// adr: 792 — profiling captures must survive park without warm-snapshot cloning.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfilingSkipsWarmSnapshotAndKeepsTerminalPark(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	sched.EnableWarmSnapshot(t, store, app.ID)
	manifest := app.Manifest
	manifest.Profiling = &api.ProfilingConfig{Enabled: true}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	id := sched.PrimeRunPlusFrameworkReady(t, store, vmm, &sched.FakeNotifier{}, e, app.ID, dep.ID)
	if err := e.Park(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if (*vmm.WarmSnapshots()) != 0 || (*vmm.Snapshots()) != 1 {
		t.Fatalf("snapshots warm=%d init=%d", (*vmm.WarmSnapshots()), (*vmm.Snapshots()))
	}
	if e.Ledger().ResidentRAM() != 0 {
		t.Fatal("profiled parked instance retained resident RAM")
	}
}

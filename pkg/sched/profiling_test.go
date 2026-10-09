package sched

// adr: 792 — profiling captures must survive park without warm-snapshot cloning.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfilingSkipsWarmSnapshotAndKeepsTerminalPark(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)
	manifest := app.Manifest
	manifest.Profiling = &api.ProfilingConfig{Enabled: true}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	id := primeRunPlusFrameworkReady(t, store, vmm, &fakeNotifier{}, e, app.ID, dep.ID)
	if err := e.Park(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if vmm.warmSnapshots != 0 || vmm.snapshots != 1 {
		t.Fatalf("snapshots warm=%d init=%d", vmm.warmSnapshots, vmm.snapshots)
	}
	if e.Ledger().ResidentRAM() != 0 {
		t.Fatal("profiled parked instance retained resident RAM")
	}
}

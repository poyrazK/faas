package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// vmmd keeps booting a base whose scan newly found a fixable CRITICAL so apps
// already serving on it keep waking (api.BaseScanCriticalBootGrace), but a new
// deployment must not start on it (deploy-path review P5): Prime refuses a
// boot that comes back flagged, with scan_critical, and frees the instance.
func TestPrimeRefusesBootAdmittedUnderBaseScanGrace(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	vmm := &sched.FakeVMM{}
	*vmm.BaseScanGrace() = true
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	err := e.Prime(ctx, app.ID, dep.ID)
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeScanCritical {
		t.Fatalf("Prime = %v, want a scan_critical refusal", err)
	}
	if *vmm.Destroys() == 0 {
		t.Fatal("the grace-admitted instance was not destroyed")
	}
	rows, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.State != string(state.StateFailed) {
			t.Fatalf("instance %s left in %s, want FAILED", row.ID, row.State)
		}
	}
}

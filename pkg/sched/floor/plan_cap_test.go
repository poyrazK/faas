package floor

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A floor saved under Scale (10) holds only Hobby's MaxMinInstances (1) once
// the subject is on Hobby: one instance is woken, and a second is not even
// though Hobby's max concurrency (2) would admit it.
// adr: 20261105120000
func TestTick_FloorSavedUnderLargerPlanIsCappedByCurrentPlan(t *testing.T) {
	store := &fakeStore{apps: []state.App{floorApp("app1", api.PlanHobby, 10)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600}
	engine := &fakeEngine{}
	tr := New(store, nil, ledger, engine, Options{
		Metrics:      wire.NewOpsMetrics("schedd"),
		PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Fatalf("Tick 1 engine.calls = %v, want one admit", engine.calls)
	}
	ledger.conc["app1"] = 1
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(engine.calls) != 1 {
		t.Fatalf("Tick 2 engine.calls = %v, want no admit above Hobby's floor of %d", engine.calls, api.PlanHobby.MaxMinInstances())
	}
}

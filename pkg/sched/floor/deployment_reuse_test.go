// adr: 20261103094355 — the floor sweep reads nothing while floors hold.

package floor

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type countingPlanResolver struct {
	fakePlanResolver
	calls int
}

func (p *countingPlanResolver) ResolvePlan(ctx context.Context, accountID string) (api.Plan, bool) {
	p.calls++
	return p.fakePlanResolver.ResolvePlan(ctx, accountID)
}

// While a floor holds, the sweep decides from the listed app and a reused
// resolution and plan, reading nothing. Before admitting it reads the app
// again, so the row's scale-out stamp, newer than the listed one, holds the
// cooldown; once the cooldown has passed it admits.
func TestTickPerDeploymentReusesSettingsUntilItAdmits(t *testing.T) {
	ctx := context.Background()
	cooled := floorApp("app1", api.PlanHobby, 2)
	cooled.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 60}
	listedApp := cooled // the list's copy has no scale-out stamp yet
	rowApp := cooled
	recent := time.Now().Add(-10 * time.Second)
	rowApp.LastScaleOutAt = &recent

	ledger := &fakeLedger{
		conc:     map[string]int{"app1": 2},
		depConc:  map[string]int{"app1\x00d1": 2},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	store := &candidateDeploymentStore{
		fakeDeploymentStore: &fakeDeploymentStore{
			deps: []state.Deployment{floorDeployment("d1", "app1", 0)},
			apps: map[string]state.App{"app1": rowApp},
		},
		candidates: []state.Deployment{floorDeployment("d1", "app1", 0)},
		appReads:   map[string]int{},
	}
	plans := &countingPlanResolver{fakePlanResolver: fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}}}
	tr := withDeploymentStore(t, &fakeStore{apps: []state.App{listedApp}}, store, ledger, engine, Options{PlanResolver: plans})

	for range 3 {
		if err := tr.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if store.appReads["app1"] != 0 || plans.calls != 1 || len(engine.calls) != 0 {
		t.Fatalf("met floor: %d app reads, %d plan reads, admissions %v; want 0, 1 and none", store.appReads["app1"], plans.calls, engine.calls)
	}

	ledger.depConc["app1\x00d1"], ledger.conc["app1"] = 1, 1
	if err := tr.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if store.appReads["app1"] != 1 || plans.calls != 2 {
		t.Fatalf("below floor: %d app reads, %d plan reads; want the app and plan read again before admitting", store.appReads["app1"], plans.calls)
	}
	if len(engine.calls) != 0 {
		t.Fatalf("admitted %v inside the row's scale-out cooldown", engine.calls)
	}

	past := time.Now().Add(-2 * time.Minute)
	rowApp.LastScaleOutAt = &past
	store.apps["app1"] = rowApp
	if err := tr.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "app1|d1" {
		t.Fatalf("admissions = %v after the cooldown, want one for app1|d1", engine.calls)
	}
}

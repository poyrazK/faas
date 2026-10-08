// spec: §5 — an explicitly parked app stays parked until a wake.

package floor

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-54): `gregale park` on an app with
// min_instances=3 left it evicted_cold while the floor kept admitting three
// instances; the reaper parked them every tick and the floor re-admitted them.
func TestTickPerDeployment_SkipsParkedApps(t *testing.T) {
	parked := floorApp("parked-app", api.PlanHobby, 3)
	parked.Status = state.AppEvictedCold
	active := floorApp("active-app", api.PlanHobby, 1)
	active.Status = state.AppActive
	appStore := &fakeStore{apps: []state.App{parked, active}}
	ledger := &fakeLedger{conc: map[string]int{}, depConc: map[string]int{}, headroom: 47_600}
	engine := &fakeEngine{}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d-parked", "parked-app", 0), floorDeployment("d-active", "active-app", 0)},
		apps: map[string]state.App{"parked-app": parked, "active-app": active},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{
		PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "active-app|d-active" {
		t.Fatalf("engine.calls = %v, want only the active app's deployment", engine.calls)
	}
}

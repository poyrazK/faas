// spec: §5 — one scheduler acts on an app's floor.

package floor

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-33): the control-plane schedd has no owner node,
// so it listed every deployment and admitted min_instances=2 for an app the
// fsn-3 schedd also floored: four instances came up and two were parked 30 s
// later. With the engine's ownership rule installed it floors only apps no
// compute schedd owns.
func TestTickPerDeployment_SkipsAppsOwnedByAPeer(t *testing.T) {
	owned := floorApp("peer-app", api.PlanHobby, 2)
	owned.NodeID = "fsn-3"
	unowned := floorApp("local-app", api.PlanHobby, 1)
	appStore := &fakeStore{apps: []state.App{owned, unowned}}
	ledger := &fakeLedger{conc: map[string]int{}, depConc: map[string]int{}, headroom: 47_600}
	engine := &fakeEngine{}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{floorDeployment("d-peer", "peer-app", 0), floorDeployment("d-local", "local-app", 0)},
		apps: map[string]state.App{"peer-app": owned, "local-app": unowned},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{
		PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
	})
	tr.WithAppOwnership(func(app state.App) bool { return app.NodeID == "" })

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "local-app|d-local" {
		t.Fatalf("engine.calls = %v, want only the unowned app's deployment", engine.calls)
	}
}

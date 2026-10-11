// adr: 072 — the inherited app floor applies only to a serving deployment.

package floor

import (
	"context"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #7 (H5-68): `--min 2` on h6-lab kept 2 warm instances
// of each of its four live deployments; three of them, an aborted canary
// among them, were at 0% traffic. A 0% sibling of a serving deployment keeps
// only an explicit per-deployment floor.
func TestTickPerDeployment_ZeroTrafficSiblingsInheritNoFloor(t *testing.T) {
	app := floorApp("split-app", api.PlanPro, 1)
	app.Status = state.AppActive
	serving := floorDeployment("d-serving", "split-app", 0)
	serving.TrafficPercent = 100
	demoted := floorDeployment("d-demoted", "split-app", 0)
	pinned := floorDeployment("d-pinned", "split-app", 1)
	other := floorDeployment("d-other-scope", "split-app", 0)
	other.Scope = "staging"
	appStore := &fakeStore{apps: []state.App{app}}
	ledger := &fakeLedger{conc: map[string]int{}, depConc: map[string]int{}, headroom: 47_600}
	engine := &fakeEngine{}
	depStore := &fakeDeploymentStore{
		deps: []state.Deployment{serving, demoted, pinned, other},
		apps: map[string]state.App{"split-app": app},
	}
	tr := withDeploymentStore(t, appStore, depStore, ledger, engine, Options{
		PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanPro}},
	})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := append([]string(nil), engine.calls...)
	sort.Strings(got)
	want := []string{"split-app|d-other-scope", "split-app|d-pinned", "split-app|d-serving"}
	if len(got) != len(want) {
		t.Fatalf("engine.calls = %v, want %v (no floor for the demoted 0%% sibling)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("engine.calls = %v, want %v", got, want)
		}
	}
}

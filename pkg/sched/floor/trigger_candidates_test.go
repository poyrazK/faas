// adr: 071 — proactive min-instances floor reconciliation.

package floor

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// candidateDeploymentStore is a fakeDeploymentStore that also lists floor
// candidates, the way state.PgStore and state.MemStore do.
type candidateDeploymentStore struct {
	*fakeDeploymentStore
	candidates     []state.Deployment
	candidateCalls int
	candidateNode  string
	appReads       map[string]int
}

func (s *candidateDeploymentStore) ListFloorDeployments(_ context.Context) ([]state.Deployment, error) {
	s.candidateCalls++
	return s.candidates, nil
}

func (s *candidateDeploymentStore) ListFloorDeploymentsByNodeID(_ context.Context, nodeID string) ([]state.Deployment, error) {
	s.candidateCalls++
	s.candidateNode = nodeID
	return s.candidates, nil
}

func (s *candidateDeploymentStore) AppByID(ctx context.Context, id string) (state.App, error) {
	s.appReads[id]++
	return s.fakeDeploymentStore.AppByID(ctx, id)
}

// TestTickPerDeploymentReadsOnlyFloorCandidates pins the production load fix:
// with a store that lists floor candidates, the sweep neither walks every
// deployment nor reads the apps that have no floor.
func TestTickPerDeploymentReadsOnlyFloorCandidates(t *testing.T) {
	ledger := &fakeLedger{
		conc:     map[string]int{"floor-app": 0},
		depConc:  map[string]int{"floor-app\x00d-floor": 0},
		headroom: 47_600,
	}
	engine := &fakeEngine{}
	base := &fakeDeploymentStore{
		deps: []state.Deployment{
			floorDeployment("d-floor", "floor-app", 0),
			floorDeployment("d-idle", "idle-app", 0),
		},
		apps: map[string]state.App{
			"floor-app": floorApp("floor-app", api.PlanHobby, 1),
			"idle-app":  floorApp("idle-app", api.PlanHobby, 0),
		},
	}
	store := &candidateDeploymentStore{
		fakeDeploymentStore: base,
		candidates:          []state.Deployment{floorDeployment("d-floor", "floor-app", 0)},
		appReads:            map[string]int{},
	}
	tr := withDeploymentStore(t, &fakeStore{}, store, ledger, engine, Options{
		PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
	})
	tr.WithOwnerNodeID("node-a")

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if store.candidateCalls != 1 || store.candidateNode != "node-a" {
		t.Fatalf("candidate listing calls = %d (node %q), want 1 for node-a", store.candidateCalls, store.candidateNode)
	}
	if base.listByIDCalls != 0 || base.listAllCalls {
		t.Fatalf("full deployment walk ran (by node %d, all %t); the candidate listing should replace it", base.listByIDCalls, base.listAllCalls)
	}
	if store.appReads["idle-app"] != 0 {
		t.Fatalf("read idle-app %d times; apps without a floor must not be read", store.appReads["idle-app"])
	}
	if len(engine.calls) != 1 || engine.calls[0] != "floor-app|d-floor" {
		t.Fatalf("engine.calls = %v, want one admission for floor-app|d-floor", engine.calls)
	}
}

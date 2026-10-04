// adr: 569
package gateway_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestPGBackendAuthoritativeEmptyWeightsPreserveOnlyExplicitTargets(t *testing.T) {
	ctx := context.Background()
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{}}
	b := gateway.NewPGBackend(nil, nil, nil).WithStore(store)
	stage := gateway.Target{InstanceID: "stage-instance", NodeID: "node", DeploymentID: "stage"}
	b.RecordTarget("app", stage)
	if picked := b.Pick("app"); picked.OK {
		t.Fatalf("target hydration synthesized default traffic: %+v", picked)
	}
	if err := b.RefreshDeploymentWeights(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	b.RecordTarget("app", stage)
	if picked := b.Pick("app"); picked.OK || b.HealthyCount("app") != 0 {
		t.Fatalf("empty production weights admitted stage traffic: %+v", picked)
	}
	if picked := b.PickForDeployment("app", "stage"); !picked.OK {
		t.Fatalf("stage target was dropped with empty weights: %+v", picked)
	}
	store.rows["app"] = []gateway.DeploymentWeightsRow{{ID: "prod", TrafficPercent: 100}}
	b.RecordTarget("app", gateway.Target{InstanceID: "prod-instance", NodeID: "node", DeploymentID: "prod"})
	if err := b.RefreshDeploymentWeights(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if picked := b.Pick("app"); !picked.OK || picked.Target.DeploymentID != "prod" {
		t.Fatalf("production weights did not activate: %+v", picked)
	}
	delete(store.rows, "app")
	if err := b.RefreshDeploymentWeights(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	b.RecordTarget("app", stage)
	if picked := b.Pick("app"); picked.OK {
		t.Fatalf("removed production weights revived stage traffic: %+v", picked)
	}
	if picked := b.PickForDeployment("app", "prod"); !picked.OK {
		t.Fatalf("retained production target disappeared: %+v", picked)
	}
}

func TestPGBackendLookupHydratesWeightsAndRetriesFailedRead(t *testing.T) {
	ctx := context.Background()
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
		"app": {{ID: "prod", TrafficPercent: 100}},
	}, err: errors.New("unavailable")}
	router := &fakeRouter{byID: map[string]gateway.App{"app.test": {ID: "app"}}}
	b := gateway.NewPGBackend(router, nil, nil).WithStore(store)
	b.RecordTarget("app", gateway.Target{InstanceID: "stage-instance", NodeID: "node", DeploymentID: "stage"})
	b.RecordTarget("app", gateway.Target{InstanceID: "prod-instance", NodeID: "node", DeploymentID: "prod"})
	if _, found := b.Lookup(ctx, "app.test"); found {
		t.Fatal("route served without authoritative weights")
	}
	store.err = nil
	if _, found := b.Lookup(ctx, "app.test"); !found {
		t.Fatal("failed weights read prevented retry")
	}
	if picked := b.Pick("app"); !picked.OK || picked.Target.DeploymentID != "prod" {
		t.Fatalf("first lookup did not select production: %+v", picked)
	}
	// A cache hit must also rehydrate weights after a target eviction.
	b.EvictTarget("app")
	b.RecordTarget("app", gateway.Target{InstanceID: "stage-instance-2", NodeID: "node", DeploymentID: "stage"})
	if _, found := b.Lookup(ctx, "app.test"); !found {
		t.Fatal("cached host could not reload weights")
	}
	if picked := b.Pick("app"); picked.OK || picked.Picked != "prod" {
		t.Fatalf("eviction replaced production weights with stage: %+v", picked)
	}
}

func TestPGBackendExactRouteSettingsDoNotReplaceProductionCache(t *testing.T) {
	ctx := context.Background()
	router := &fakeRouter{byID: map[string]gateway.App{
		"app.test": {ID: "app", MaxConcurrency: 2},
		"stage.test": {ID: "app", MaxConcurrency: 8, MaintenanceMode: true,
			PinnedDeploymentID: "stage", PinnedDeploymentScope: "staging"},
	}}
	b := gateway.NewPGBackend(router, nil, nil)
	for i := 0; i < 2; i++ {
		if stage, found := b.Lookup(ctx, "stage.test"); !found || stage.MaxConcurrency != 8 || !stage.MaintenanceMode {
			t.Fatalf("stage lookup = %+v, found=%v", stage, found)
		}
		if prod, found := b.Lookup(ctx, "app.test"); !found || prod.MaxConcurrency != 2 || prod.MaintenanceMode || prod.PinnedDeploymentID != "" {
			t.Fatalf("stage replaced production cache: %+v, found=%v", prod, found)
		}
	}
}

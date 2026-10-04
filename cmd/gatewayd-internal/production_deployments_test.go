// adr: 583
package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type routingDeploymentStore struct{ deployments []state.Deployment }

func (s routingDeploymentStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return s.deployments, nil
}

func TestProductionWeightsExcludeStagesAndPreserveLegacyUntilCutover(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps []state.Deployment
		want []gateway.DeploymentWeightsRow
	}{
		{"stage only", []state.Deployment{{ID: "stage", Scope: "staging", TrafficPercent: 100}}, nil},
		{"legacy with stage", []state.Deployment{
			{ID: "stage", Scope: "staging", TrafficPercent: 100},
			{ID: "legacy", Scope: "default", TrafficPercent: 100},
		}, []gateway.DeploymentWeightsRow{{ID: "legacy", TrafficPercent: 100}}},
		{"dark production candidate", []state.Deployment{
			{ID: "candidate", Scope: "production", TrafficPercent: 0},
			{ID: "legacy", Scope: "default", TrafficPercent: 100},
		}, []gateway.DeploymentWeightsRow{{ID: "legacy", TrafficPercent: 100}}},
		{"production cutover", []state.Deployment{
			{ID: "stage", Scope: "staging", TrafficPercent: 100},
			{ID: "legacy", Scope: "default", TrafficPercent: 100},
			{ID: "prod-a", Scope: "production", TrafficPercent: 25},
			{ID: "prod-b", Scope: "production", TrafficPercent: 75},
		}, []gateway.DeploymentWeightsRow{{ID: "prod-a", TrafficPercent: 25}, {ID: "prod-b", TrafficPercent: 75}}},
		{"empty legacy label", []state.Deployment{
			{ID: "stage", Scope: "pr-42", TrafficPercent: 100},
			{ID: "legacy", TrafficPercent: 100},
		}, []gateway.DeploymentWeightsRow{{ID: "legacy", TrafficPercent: 100}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := (weightsStoreAdapter{store: routingDeploymentStore{tc.deps}}).LiveDeployments(context.Background(), "app")
			if err != nil || len(rows) != len(tc.want) || (len(rows) > 0 && !reflect.DeepEqual(rows, tc.want)) {
				t.Fatalf("production weights = %+v, %v; want %+v", rows, err, tc.want)
			}
		})
	}
}

func TestProductionRoutingIgnoresStageQuarantineAndSidecars(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := seedApp(t, store, "isolated-routing", api.PlanPro)
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive,
		Sidecars: json.RawMessage(`[{"name":"proxy","type":"sidecar","port":8081,"primary_ingress":true}]`)})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive,
		Sidecars: json.RawMessage(`[{"name":"proxy","type":"sidecar","port":9090,"primary_ingress":true}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentParked(ctx, staging.ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
	resolved, found, err := router.ResolveHost(ctx, app.Slug+".gregale.dev")
	if err != nil || !found || resolved.SecurityQuarantined || resolved.PrimaryIngressPort != 8081 {
		t.Fatalf("production affected by stage: %+v, found=%v, err=%v", resolved, found, err)
	}
	backend := gateway.NewPGBackend(router, nil, testLogger()).WithStore(weightsStoreAdapter{store: store})
	// An out-of-band stage wake can precede production's first host lookup.
	backend.RecordTarget(app.ID, gateway.Target{InstanceID: "stage-instance", NodeID: "stage-node", DeploymentID: staging.ID})
	backend.RecordTarget(app.ID, gateway.Target{InstanceID: "prod-instance", NodeID: "prod-node", DeploymentID: production.ID})
	if _, found := backend.Lookup(ctx, app.Slug+".gregale.dev"); !found {
		t.Fatal("production host did not resolve")
	}
	for i := 0; i < 100; i++ {
		if picked := backend.Pick(app.ID); !picked.OK || picked.Target.DeploymentID != production.ID {
			t.Fatalf("production picked stage: %+v", picked)
		}
	}
	if picked := backend.PickForDeployment(app.ID, staging.ID); !picked.OK || picked.Target.DeploymentID != staging.ID {
		t.Fatalf("explicit stage target unavailable: %+v", picked)
	}
}

func TestDarkPreviewRoutesUseTheirOwnSidecars(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := seedApp(t, store, "dark-sidecar", api.PlanPro)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive,
		TrafficPercentExplicit: true,
		Sidecars:               json.RawMessage(`[{"name":"proxy","type":"sidecar","port":9090,"primary_ingress":true}]`)})
	if err != nil {
		t.Fatal(err)
	}
	resolved, found, err := (pgRouter{store: store}).deploymentPreview(ctx, app.Slug, deployment.Revision)
	if err != nil || !found || resolved.PrimaryIngressPort != 9090 || len(resolved.Sidecars) != 1 {
		t.Fatalf("dark candidate lost sidecars: %+v, found=%v, err=%v", resolved, found, err)
	}
}

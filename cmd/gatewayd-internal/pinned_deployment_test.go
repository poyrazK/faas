package main

// adr: 198

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPinnedDeploymentStatusThroughProductionAdapter wires the backend the
// way run.go does. The H8-7 refusal ("deployment v1 is superseded and no
// longer serves traffic") first shipped behind a type assertion the
// production adapter never satisfied, so the gateway kept answering
// "App concurrency reached" for an alias to a superseded deployment.
func TestPinnedDeploymentStatusThroughProductionAdapter(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := seedApp(t, store, "pinned-status", api.PlanPro)
	old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	old, err = store.DeploymentByID(ctx, old.ID)
	if err != nil || old.Status != state.DeploySuperseded {
		t.Fatalf("old deployment = %+v, %v; want superseded", old, err)
	}

	backend := gateway.NewPGBackend(pgRouter{store: store}, nil, testLogger()).WithStore(weightsStoreAdapter{store: store})
	for _, tc := range []struct {
		name, id, status string
		serving          bool
	}{
		{name: "superseded revision", id: old.ID, status: string(state.DeploySuperseded), serving: false},
		{name: "live revision", id: current.ID, status: string(state.DeployLive), serving: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serving, label, status, found := backend.PinnedDeploymentStatus(ctx, tc.id)
			if !found || serving != tc.serving || status != tc.status || label == "" {
				t.Fatalf("PinnedDeploymentStatus = serving %v label %q status %q found %v; want serving %v status %q found true",
					serving, label, status, found, tc.serving, tc.status)
			}
		})
	}
	if _, _, _, found := backend.PinnedDeploymentStatus(ctx, "missing-deployment"); found {
		t.Fatal("an unknown deployment must report not found, never a refusal")
	}
}

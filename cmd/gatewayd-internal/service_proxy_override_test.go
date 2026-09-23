package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyDeploymentValidatorLiveMembership(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := seedApp(t, store, "override-orders", api.PlanPro)
	foreign := seedApp(t, store, "override-other", api.PlanPro)
	create := func(appID, digest string, percent int) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: digest,
			Status: state.DeployPending, TrafficPercent: percent, TrafficPercentExplicit: true,
		})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatalf("MarkDeploymentLive: %v", err)
		}
		return deployment
	}
	stable := create(app.ID, "sha256:stable", 100)
	dark := create(app.ID, "sha256:dark", 0)
	other := create(foreign.ID, "sha256:foreign", 100)
	validator := newServiceProxyDeploymentValidator(store)
	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{
		{"stable", stable.ID, true},
		{"zero-percent live", dark.ID, true},
		{"foreign app", other.ID, false},
		{"missing", "00000000-0000-4000-8000-000000000000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validator(ctx, app.ID, tc.id)
			if err != nil || got != tc.want {
				t.Fatalf("validate = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if err := store.MarkDeploymentSuperseded(ctx, dark.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}
	if got, err := validator(ctx, app.ID, dark.ID); err != nil || got {
		t.Fatalf("superseded deployment = %v, %v; want false", got, err)
	}
}

type failingLiveDeploymentsStore struct {
	state.Store
	err error
}

type unfilteredLiveDeploymentsStore struct {
	state.Store
	deployments []state.Deployment
}

func (s unfilteredLiveDeploymentsStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return s.deployments, nil
}

func TestServiceProxyDeploymentValidatorChecksRowsDefensively(t *testing.T) {
	const deploymentID = "22222222-2222-4222-8222-222222222222"
	for _, row := range []state.Deployment{
		{ID: deploymentID, AppID: "app-1", Status: state.DeployPending},
		{ID: deploymentID, AppID: "app-2", Status: state.DeployLive},
	} {
		validator := newServiceProxyDeploymentValidator(unfilteredLiveDeploymentsStore{
			Store: state.NewMemStore(), deployments: []state.Deployment{row},
		})
		if valid, err := validator(context.Background(), "app-1", deploymentID); valid || err != nil {
			t.Fatalf("row %+v validated = %v, %v; want false", row, valid, err)
		}
	}
}

func (s failingLiveDeploymentsStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return nil, s.err
}

func TestServiceProxyDeploymentValidatorStoreError(t *testing.T) {
	boom := errors.New("store unavailable")
	validator := newServiceProxyDeploymentValidator(failingLiveDeploymentsStore{Store: state.NewMemStore(), err: boom})
	if valid, err := validator(context.Background(), "app-1", "dep-1"); valid || !errors.Is(err, boom) {
		t.Fatalf("validate = %v, %v; want wrapped store error", valid, err)
	}
}

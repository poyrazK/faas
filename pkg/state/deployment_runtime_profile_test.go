//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSetDeploymentRuntimeProfile_ImagePortHandoff(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "oci-port-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{
				AccountID: acct.ID, Slug: "oci-port-" + backend, Type: state.AppTypeApp,
				RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
			})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:oci-port", Status: state.DeployPending,
			})
			if err != nil {
				t.Fatal(err)
			}
			profile := []byte(`{"version":"v1","framework":"unknown","port":5678,"health_path":"","inferred":false}`)
			if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, profile); err != nil {
				t.Fatal(err)
			}
			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(got.InferredProfile) || got.OverridePort != 0 {
				t.Fatalf("runtime profile=%s override_port=%d", got.InferredProfile, got.OverridePort)
			}
			var persisted struct {
				Port int `json:"port"`
			}
			if err := json.Unmarshal(got.InferredProfile, &persisted); err != nil || persisted.Port != 5678 {
				t.Fatalf("persisted profile port=%d err=%v", persisted.Port, err)
			}
			if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, []byte(`{bad`)); err == nil {
				t.Fatal("invalid profile was accepted")
			}
			sourceDep, err := store.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRuntimeProfile(ctx, sourceDep.ID, profile); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("source profile overwrite error=%v, want invalid state transition", err)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, profile); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("terminal profile write error=%v, want invalid state transition", err)
			}
		})
	}
}

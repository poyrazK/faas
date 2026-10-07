//go:build !no_pg

// adr: 682
package state_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrozenImageHealthcheckSurvivesProfileUpdatesAndRetries(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "frozen-health-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "frozen-health", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []*api.ComposeHealthcheck{nil, {Test: []string{"NONE"}}, {TimeoutNS: int64(250 * time.Millisecond)}} {
				profile, err := frameworkprofile.CaptureImageRuntime(nil, check)
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.com/app:v1", InferredProfile: profile})
				if err != nil {
					t.Fatal(err)
				}
				for _, update := range []string{`{"port":3000}`, `{"port":4000,"image_healthcheck":{"version":"v1","override":{"test":["CMD","/tampered"]}}}`} {
					if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, []byte(update)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeImageManifestInvalid, "retry fixture"); err != nil {
					t.Fatal(err)
				}
				retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
				if err != nil {
					t.Fatal(err)
				}
				captured, err := frameworkprofile.ImageHealthcheckFromProfile(retry.InferredProfile)
				if err != nil || captured == nil || !reflect.DeepEqual(captured.Override, check) {
					t.Fatalf("retry lost healthcheck: %s, %v", retry.InferredProfile, err)
				}
			}
			legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.com/app:v1"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRuntimeProfile(ctx, legacy.ID, []byte(`{"port":3000,"image_healthcheck":{"version":"v1","override":null}}`)); err != nil {
				t.Fatal(err)
			}
			stored, err := store.DeploymentByID(ctx, legacy.ID)
			if err != nil {
				t.Fatal(err)
			}
			if captured, err := frameworkprofile.ImageHealthcheckFromProfile(stored.InferredProfile); err != nil || captured != nil {
				t.Fatalf("worker introduced a healthcheck contract: %s, %v", stored.InferredProfile, err)
			}
		})
	}
}

//go:build !no_pg

// adr: 686
package state_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrozenImageCommandSurvivesProfileUpdatesAndRetries(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "frozen-cmd-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "frozen-cmd-" + backend, Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range [][]string{nil, {"serve", "with spaces"}, {}} {
				profile, err := frameworkprofile.CaptureImageCommand(command)
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
					Status: state.DeployPending, ImageDigest: "example.com/app@sha256:" + strings.Repeat("a", 64), InferredProfile: profile})
				if err != nil {
					t.Fatal(err)
				}
				for _, update := range []string{
					`{"version":"v1","framework":"unknown","port":3000}`,
					`{"version":"v1","port":4000,"image_command":{"version":"v1","cmd":["tampered"]}}`,
				} {
					if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, []byte(update)); err != nil {
						t.Fatal(err)
					}
					stored, err := store.DeploymentByID(ctx, dep.ID)
					if err != nil {
						t.Fatal(err)
					}
					assertFrozenImageCommand(t, stored, command)
				}
				if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeImageManifestInvalid, "retry fixture"); err != nil {
					t.Fatal(err)
				}
				retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
				if err != nil || retry.ID == dep.ID {
					t.Fatalf("retry = %+v, %v", retry, err)
				}
				assertFrozenImageCommand(t, retry, command)
				var current struct{ Port int }
				if err := json.Unmarshal(retry.InferredProfile, &current); err != nil || current.Port != 4000 {
					t.Fatalf("port handoff lost: %s, %v", retry.InferredProfile, err)
				}
			}
			legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				Status: state.DeployPending, ImageDigest: "example.com/app@sha256:" + strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRuntimeProfile(ctx, legacy.ID, []byte(`{"port":3000,"image_command":{"version":"v1","cmd":["injected"]}}`)); err != nil {
				t.Fatal(err)
			}
			stored, err := store.DeploymentByID(ctx, legacy.ID)
			if err != nil {
				t.Fatal(err)
			}
			captured, err := frameworkprofile.ImageCommandFromProfile(stored.InferredProfile)
			if err != nil || captured != nil {
				t.Fatalf("worker introduced an admission contract: %s, %v", stored.InferredProfile, err)
			}
		})
	}
}

func assertFrozenImageCommand(t *testing.T, dep state.Deployment, command []string) {
	t.Helper()
	captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
	if err != nil || captured == nil || !reflect.DeepEqual(captured.Cmd, command) {
		t.Fatalf("captured image command = %s, %v; want %#v", dep.InferredProfile, err, command)
	}
}

//go:build !no_pg

// adr: 20261019142544, 20261019195000
package state_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stage retry rebuilds a source deployment with the build settings its plan
// froze, not the app's current ones: the Dockerfile path, the Compose build
// arguments and target, and the Compose healthcheck.
func TestStageRetryKeepsFrozenSourceBuildSettings(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "retry-source-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "retry-source", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			frozen := frameworkprofile.Profile{
				Version: frameworkprofile.Version, Framework: "docker", DockerfilePath: "Dockerfile.prod",
				DockerfileBuild:  &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"NODE_ENV": "production"}},
				ImageHealthcheck: &frameworkprofile.ImageHealthcheck{Version: frameworkprofile.ImageHealthcheckVersion, Override: &api.ComposeHealthcheck{Test: []string{"CMD", "/check"}}},
			}
			profile, err := json.Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: "/spool/source.tar.gz", InferredProfile: profile})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeDeployFailed, "retry fixture"); err != nil {
				t.Fatal(err)
			}
			retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
			if err != nil {
				t.Fatal(err)
			}
			var kept frameworkprofile.Profile
			if err := json.Unmarshal(retry.InferredProfile, &kept); err != nil {
				t.Fatalf("decode retried profile %s: %v", retry.InferredProfile, err)
			}
			if kept.DockerfilePath != frozen.DockerfilePath || !reflect.DeepEqual(kept.DockerfileBuild, frozen.DockerfileBuild) ||
				kept.ImageHealthcheck == nil || !reflect.DeepEqual(kept.ImageHealthcheck.Override, frozen.ImageHealthcheck.Override) {
				t.Fatalf("retry lost frozen build settings: %s", retry.InferredProfile)
			}
		})
	}
}

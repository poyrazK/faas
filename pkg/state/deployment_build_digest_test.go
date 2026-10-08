//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceBuildDigestFencesExportAndTaskIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "build-digest-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "build-digest-" + backend, Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging})
			if err != nil {
				t.Fatal(err)
			}
			const export = "/builder/export.tar"
			if err := store.SetDeploymentRootfs(ctx, dep.ID, export, "builder/export", 100); err != nil {
				t.Fatal(err)
			}
			pins := store.(state.DeploymentBuildDigestStore)
			digest, other := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
			if err := pins.PinDeploymentBuildDigest(ctx, dep.ID, export+"-stale", digest); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("stale export pinned a task identity: %v", err)
			}
			for i := 0; i < 2; i++ {
				if err := pins.PinDeploymentBuildDigest(ctx, dep.ID, export, digest); err != nil {
					t.Fatal(err)
				}
			}
			if err := pins.PinDeploymentBuildDigest(ctx, dep.ID, export, other); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("changed the immutable task identity: %v", err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/apps/layer.ext4", "apps/layer.ext4", 200); err != nil {
				t.Fatal(err)
			}
			if err := pins.PinDeploymentBuildDigest(ctx, dep.ID, export, digest); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("stale worker reused consumed export: %v", err)
			}
			task, err := store.(state.AppTaskStore).CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{"/app/check"}})
			if err != nil || task.ImageDigest != digest || task.ArtifactKey != "apps/layer.ext4" {
				t.Fatalf("source task identity: digest=%q artifact=%q err=%v", task.ImageDigest, task.ArtifactKey, err)
			}
			if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeDeployFailed, "fixture failure"); err != nil {
				t.Fatal(err)
			}
			if err := pins.PinDeploymentBuildDigest(ctx, dep.ID, "/apps/layer.ext4", digest); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("modified terminal source deployment: %v", err)
			}
		})
	}
}

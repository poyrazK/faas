//go:build !no_pg

// adr: 678
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPinDeploymentImageReference(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			acct, err := store.CreateAccount(ctx, "image-pin-"+backend+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "image-pin-" + backend, Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			tag := "example.com/app:v1"
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: tag})
			if err != nil {
				t.Fatal(err)
			}
			pins := store.(state.DeploymentImageReferenceStore)
			pinned := "example.com/app@sha256:" + strings.Repeat("a", 64)
			other := "example.com/app@sha256:" + strings.Repeat("b", 64)
			if err := pins.PinDeploymentImageReference(ctx, dep.ID, tag, tag); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("accepted mutable target: %v", err)
			}
			if err := pins.PinDeploymentImageReference(ctx, dep.ID, tag, pinned); err != nil {
				t.Fatal(err)
			}
			if err := pins.PinDeploymentImageReference(ctx, dep.ID, tag, other); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("stale worker changed pinned source: %v", err)
			}
			if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeImageNotFound, "failed after resolution"); err != nil {
				t.Fatal(err)
			}
			if err := pins.PinDeploymentImageReference(ctx, dep.ID, pinned, other); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("changed terminal deployment: %v", err)
			}
			retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageSourceDownload)
			if err != nil || retry.ImageDigest != pinned {
				t.Fatalf("retry = %+v, %v; want immutable source", retry, err)
			}
		})
	}
}

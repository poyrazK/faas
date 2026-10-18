// adr: 1741 — an app's runtime generation moves with every change a wake
// depends on, and stays put for bookkeeping and for other apps.
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemAppRuntimeGeneration(t *testing.T) {
	testAppRuntimeGeneration(t, state.NewMemStore())
}

func testAppRuntimeGeneration(t *testing.T, store runtimeAppEnvTestStore) {
	generations := store.(state.AppRuntimeGenerationReader)
	ram, scale := 512, api.PlanScale
	generationOf := func(t *testing.T, appID string) int64 {
		t.Helper()
		g, err := generations.AppRuntimeGeneration(t.Context(), appID)
		if err != nil {
			t.Fatalf("generation of %s: %v", appID, err)
		}
		return g
	}
	for _, tc := range []struct {
		name   string
		change func(context.Context, runtimeAppEnvFixture) error
		moves  bool
	}{
		{name: "app resized", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			_, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{RAMMB: &ram})
			return err
		}},
		{name: "environment value written", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE", "edited")
		}},
		{name: "environment value deleted", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.DeleteAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE")
		}},
		{name: "secret written", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", []byte("value"))
		}},
		{name: "runtime config stamped", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.MarkAppRuntimeConfigChanged(ctx, f.app.ID)
		}},
		{name: "deployment goes live", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.MarkDeploymentLive(ctx, f.deployments["production"].ID)
		}},
		{name: "deployment created", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			_, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			return err
		}},
		{name: "account suspended", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.UpdateAccountStatus(ctx, f.account.ID, state.AccountSuspended)
		}},
		{name: "plan changed", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.UpdateAccountPlan(ctx, f.account.ID, scale)
		}},
		{name: "project environment removed", moves: true, change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "other")
		}},
		{name: "snapshot miss recorded", change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.DeploymentRecordSnapshotMiss(ctx, f.deployments["production"].ID, time.Now().Add(time.Minute))
		}},
		{name: "scale-in stamped", change: func(ctx context.Context, f runtimeAppEnvFixture) error {
			return store.StampAppScaleIn(ctx, f.app.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := seedRuntimeAppEnv(t, store)
			bystander := seedRuntimeAppEnv(t, store)
			before, other := generationOf(t, f.app.ID), generationOf(t, bystander.app.ID)
			if err := tc.change(ctx, f); err != nil {
				t.Fatalf("change: %v", err)
			}
			if moved := generationOf(t, f.app.ID) != before; moved != tc.moves {
				t.Fatalf("generation moved = %v after %s, want %v", moved, tc.name, tc.moves)
			}
			if generationOf(t, bystander.app.ID) != other {
				t.Fatalf("%s moved another account's app", tc.name)
			}
		})
	}

	t.Run("admission insert requires the guarded generation", func(t *testing.T) {
		ctx := t.Context()
		f := seedRuntimeAppEnv(t, store)
		node := runtimeSecretNodeForTest(t, store)
		dep := f.deployments["production"]
		generation := generationOf(t, f.app.ID)
		guarded := state.WithRuntimeGenerationGuard(ctx, f.app.ID, generation)
		if _, err := store.CreateInstance(guarded, f.app.ID, dep.ID, string(state.StateColdBooting), 256, node, uuid.NewString()); err != nil {
			t.Fatalf("admission at the current generation: %v", err)
		}
		if err := store.MarkAppRuntimeConfigChanged(ctx, f.app.ID); err != nil {
			t.Fatal(err)
		}
		before, err := store.ListInstancesForApp(ctx, f.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateInstance(guarded, f.app.ID, dep.ID, string(state.StateColdBooting), 256, node, uuid.NewString())
		if !errors.Is(err, state.ErrRuntimeGenerationMoved) || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("admission after the generation moved = %v, want ErrRuntimeGenerationMoved", err)
		}
		if after, err := store.ListInstancesForApp(ctx, f.app.ID); err != nil || len(after) != len(before) {
			t.Fatalf("a refused admission left a row: %d -> %d, %v", len(before), len(after), err)
		}
	})

	t.Run("account change moves every app it owns", func(t *testing.T) {
		ctx := t.Context()
		f := seedRuntimeAppEnv(t, store)
		sibling, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "sibling-" + f.app.ID[:8],
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		before, siblingBefore := generationOf(t, f.app.ID), generationOf(t, sibling.ID)
		if err := store.UpdateAccountStatus(ctx, f.account.ID, state.AccountSuspended); err != nil {
			t.Fatal(err)
		}
		if generationOf(t, f.app.ID) == before || generationOf(t, sibling.ID) == siblingBefore {
			t.Fatal("suspending the account left one of its apps' generations unchanged")
		}
	})
}

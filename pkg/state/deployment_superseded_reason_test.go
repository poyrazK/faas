package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestOutpacedDeploymentSaysWhyContract pins Hunt 31 H31-3: a deployment that
// a newer revision overtook before it went live (ADR-681) showed only
// "superseded" with its canary rollout still pending. It now names the newer
// deployment, and an in-flight canary rollout ends as aborted.
func TestOutpacedDeploymentSaysWhyContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, tc := range []struct {
			name        string
			canarySteps int
			wantRollout string
		}{
			{name: "canary", canarySteps: 4, wantRollout: "aborted"},
			{name: "immediate", canarySteps: 0, wantRollout: "pending"},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				ctx := context.Background()
				if backend == "postgres" {
					store, ctx = pgStore(t)
				}
				acct, err := store.CreateAccount(ctx, "outpaced-reason@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "outpaced-reason", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				older, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:older", Scope: "default",
					Status: state.DeployBuilding, CanaryTotalSteps: tc.canarySteps, RolloutState: "pending"})
				if err != nil {
					t.Fatal(err)
				}
				// In progress, as in production: a newer deployment supersedes
				// only queued ones when it is created, so this one meets the
				// fence when it tries to go live.
				if err := store.UpdateDeploymentStatus(ctx, older.ID, state.DeploySnapshotting, ""); err != nil {
					t.Fatal(err)
				}
				newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer", Scope: "default"})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLiveIfLatest(ctx, older.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
					t.Fatalf("MarkDeploymentLiveIfLatest(older) = %v, want ErrDeploymentSuperseded", err)
				}
				got, err := store.DeploymentByID(ctx, older.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantError := "deployment " + newer.ID + " was deployed after this one, so this one did not go live"
				if got.Status != state.DeploySuperseded || got.ErrorCode != state.DeploymentSupersededCode || got.Error != wantError {
					t.Fatalf("older = %s %q %q; want superseded %q %q", got.Status, got.ErrorCode, got.Error, state.DeploymentSupersededCode, wantError)
				}
				if state.NormalizeRolloutState(got.RolloutState) != tc.wantRollout {
					t.Fatalf("older rollout = %q, want %q", got.RolloutState, tc.wantRollout)
				}
			})
		}
	}
}

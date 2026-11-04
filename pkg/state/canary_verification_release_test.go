package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestReleaseCanariesForVerificationContract pins the ADR-20261030102813
// amendment (Hunt 31 H31-4) in both stores: a newer candidate that cannot be
// woken beside the serving deployment and an older in-flight canary takes the
// canary's rollout grant; one that fits, or that is older than the canary,
// releases nothing.
func TestReleaseCanariesForVerificationContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, tc := range []struct {
			name           string
			maxReleases    int
			candidateFirst bool
			wantReleased   bool
		}{
			{name: "candidate does not fit", maxReleases: 2, wantReleased: true},
			{name: "candidate fits", maxReleases: 3},
			{name: "canary is newer than the candidate", maxReleases: 2, candidateFirst: true},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				ctx := context.Background()
				if backend == "postgres" {
					store, ctx = pgStore(t)
				}
				releaser := store.(state.CanaryVerificationReleaser)
				acct, err := store.CreateAccount(ctx, "canary-verify@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "canary-verify", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable", Scope: "default"})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
					t.Fatal(err)
				}
				newCandidate := func() state.Deployment {
					t.Helper()
					dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: "default"})
					if err != nil {
						t.Fatal(err)
					}
					return dep
				}
				var candidate state.Deployment
				if tc.candidateFirst {
					candidate = newCandidate()
				}
				canary, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:canary", Scope: "default",
					CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, canary.ID); err != nil {
					t.Fatal(err)
				}
				if !tc.candidateFirst {
					candidate = newCandidate()
				}

				released, err := releaser.ReleaseCanariesForVerification(ctx, candidate.ID, tc.maxReleases)
				if err != nil {
					t.Fatal(err)
				}
				gotCanary, err := store.DeploymentByID(ctx, canary.ID)
				if err != nil {
					t.Fatal(err)
				}
				gotStable, err := store.DeploymentByID(ctx, stable.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.wantReleased {
					if len(released) != 0 || gotCanary.Status != state.DeployLive || gotCanary.TrafficPercent != 1 || gotStable.TrafficPercent != 99 {
						t.Fatalf("released=%v canary=%s %d%% stable=%d%%; want nothing released", released, gotCanary.Status, gotCanary.TrafficPercent, gotStable.TrafficPercent)
					}
					return
				}
				if len(released) != 1 || released[0] != canary.ID {
					t.Fatalf("released = %v, want [%s]", released, canary.ID)
				}
				if gotCanary.Status != state.DeploySuperseded || gotCanary.TrafficPercent != 0 || gotCanary.RolloutState != "aborted" ||
					gotCanary.RolloutAbortedReason != "superseded by deployment "+candidate.ID {
					t.Fatalf("canary = %s %d%% %s %q; want superseded, 0, aborted by the candidate", gotCanary.Status, gotCanary.TrafficPercent, gotCanary.RolloutState, gotCanary.RolloutAbortedReason)
				}
				if gotStable.Status != state.DeployLive || gotStable.TrafficPercent != 100 {
					t.Fatalf("stable = %s %d%%, want live 100%%", gotStable.Status, gotStable.TrafficPercent)
				}
			})
		}
	}
}

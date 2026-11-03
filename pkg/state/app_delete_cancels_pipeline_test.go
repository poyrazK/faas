package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestScheduleAppDeletionCancelsInProgressDeploysContract pins Hunt 31
// H31-12: deleting an app left a deploy caught mid-activation "snapshotting"
// for the whole restore window and its builds running. Both deletion paths
// now cancel in-progress deploys and their builds, as SoftDeleteAppCascade
// already did. The live deployment stays, so a restore brings the app back on
// it.
func TestScheduleAppDeletionCancelsInProgressDeploysContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, withActivity := range []bool{false, true} {
			name := backend + "/plain"
			if withActivity {
				name = backend + "/with_activity"
			}
			t.Run(name, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				ctx := context.Background()
				if backend == "postgres" {
					store, ctx = pgStore(t)
				}
				// The activity path needs an organization-owned app.
				created, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "delete-cancels@example.com", Plan: api.PlanPro})
				if err != nil {
					t.Fatal(err)
				}
				acct := created.Account
				actorID, err := uuid.Parse(acct.ID)
				if err != nil {
					t.Fatal(err)
				}
				activity := func(kind, source string) state.OrgActivity {
					return state.OrgActivity{Kind: kind, ActorType: state.OrgActivityActorUser, ActorAccountID: &actorID, ActorLabel: acct.Email,
						ResourceType: "app", SourceType: kind, SourceID: source, Data: []byte(`{"phase":"` + source + `"}`)}
				}
				lifecycle := store.(state.OrgActivityAppLifecycleMutationStore)
				app, _, err := lifecycle.CreateAppIfUnderQuotaWithActivity(ctx, state.App{AccountID: acct.ID, Slug: "delete-cancels", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60},
					api.MustLimitsFor(api.PlanPro), activity("app.created", "created"))
				if err != nil {
					t.Fatal(err)
				}
				live, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:live", Scope: "default"})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, live.ID); err != nil {
					t.Fatal(err)
				}
				inflight, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Scope: "default", Status: state.DeployBuilding})
				if err != nil {
					t.Fatal(err)
				}
				build, err := store.CreateBuild(ctx, inflight.ID, state.DeploymentKindTarball, 1, "")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateDeploymentStatus(ctx, inflight.ID, state.DeploySnapshotting, ""); err != nil {
					t.Fatal(err)
				}

				grace := time.Now().UTC().Add(time.Hour)
				if withActivity {
					_, _, err = lifecycle.ScheduleAppDeletionWithActivity(ctx, app.ID, grace, activity("app.deleted", "deleted"))
				} else {
					_, err = store.ScheduleAppDeletion(ctx, app.ID, grace)
				}
				if err != nil {
					t.Fatal(err)
				}

				gotInflight, err := store.DeploymentByID(ctx, inflight.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotInflight.Status != state.DeployCancelled || gotInflight.CancelledByPrincipal != "system:app-delete" || gotInflight.CancelReason != string(state.CancelReasonSystem) {
					t.Fatalf("in-progress deploy after delete = %s by %q (%q); want cancelled by system:app-delete", gotInflight.Status, gotInflight.CancelledByPrincipal, gotInflight.CancelReason)
				}
				gotBuild, err := store.BuildByID(ctx, build.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotBuild.Status != state.BuildCancelled || !gotBuild.CancelledByDeploymentCascade {
					t.Fatalf("build after delete = %s cascade=%v; want cancelled by the cascade", gotBuild.Status, gotBuild.CancelledByDeploymentCascade)
				}
				gotLive, err := store.DeploymentByID(ctx, live.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotLive.Status != state.DeployLive {
					t.Fatalf("live deploy after delete = %s, want live", gotLive.Status)
				}

				limits, _ := api.LimitsFor(api.PlanPro)
				if _, err := store.RestoreApp(ctx, app.ID, limits); err != nil {
					t.Fatal(err)
				}
				gotLive, _ = store.DeploymentByID(ctx, live.ID)
				gotInflight, _ = store.DeploymentByID(ctx, inflight.ID)
				if gotLive.Status != state.DeployLive || gotInflight.Status != state.DeployCancelled {
					t.Fatalf("after restore: live=%s in-progress=%s; want live and cancelled", gotLive.Status, gotInflight.Status)
				}
			})
		}
	}
}

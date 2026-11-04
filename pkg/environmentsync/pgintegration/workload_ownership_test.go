package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func prepareOwnershipSourceCandidate(t *testing.T, basic gitOpsTestStore, store intentTestStore, lease state.EnvironmentGitOpsLease) {
	t.Helper()
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	prep := basic.(state.EnvironmentGitOpsPreparationStore)
	requests, err := prep.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	for _, request := range requests {
		artifacts[request.Resource] = sourceArtifact(t, request)
	}
	if _, err := prep.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, artifacts); err != nil {
		t.Fatal(err)
	}
}

func manualOwnershipDeployment(t *testing.T, store state.Store, appID, scope string, canary bool) state.Deployment {
	t.Helper()
	input := state.Deployment{AppID: appID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "registry.example/manual@sha256:" + strings.Repeat("f", 64), Status: state.DeployPending}
	if canary {
		input.CanaryPreset = "balanced"
		input.CanaryTotalSteps = 4
		input.RolloutState = "pending"
		input.TrafficPercent = 1
	}
	dep, err := store.CreateDeployment(t.Context(), input)
	if err != nil {
		t.Fatal("manual create", err)
	}
	if err := store.SetDeploymentRootfs(t.Context(), dep.ID, "/manual.ext4", "manual/"+dep.ID, 4096); err != nil {
		t.Fatal("manual rootfs", err)
	}
	if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal("manual promote", err)
	}
	dep, err = store.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestGitOpsOwnershipDoesNotBlockOtherEnvironmentTraffic(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, app, original, _ := appliedSourceCandidateFixture(t, basic, "source")
		manual := manualOwnershipDeployment(t, basic.(state.Store), app.ID, "staging", false)
		if _, err := basic.(state.Store).UpdateDeploymentTraffic(t.Context(), manual.ID, 100); err != nil {
			t.Fatal("baseline staging traffic", err)
		}
		prepareOwnershipSourceCandidate(t, basic, store, lease)
		if _, err := basic.(state.Store).UpdateDeploymentTraffic(t.Context(), original.ID, 100); !errors.Is(err, state.ErrConflict) {
			t.Fatal("production traffic escaped its bound source", err)
		}
		if _, err := basic.(state.Store).UpdateDeploymentTraffic(t.Context(), manual.ID, 100); err != nil {
			t.Fatalf("production GitOps candidate blocked manual staging traffic: %v", err)
		}
	})
}

func TestGitOpsOwnershipDoesNotBlockOtherEnvironmentCanary(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, app, _, _ := appliedSourceCandidateFixture(t, basic, "source")
		manualOwnershipDeployment(t, basic.(state.Store), app.ID, "staging", false)
		canary := manualOwnershipDeployment(t, basic.(state.Store), app.ID, "staging", true)
		params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "review"}}
		if _, _, err := basic.(state.CanaryAdvancer).AdvanceCanary(t.Context(), canary.ID, params); err != nil {
			t.Fatal("baseline staging canary", err)
		}
		prepareOwnershipSourceCandidate(t, basic, store, lease)
		params.ExpectedStep = 1
		params.TrafficPercent = 50
		if _, _, err := basic.(state.CanaryAdvancer).AdvanceCanary(t.Context(), canary.ID, params); err != nil {
			t.Fatalf("production GitOps candidate blocked manual staging canary: %v", err)
		}
	})
}

func TestGitOpsDetachRestoresManualDeploymentOwnership(t *testing.T) {
	for _, operation := range []string{"detach", "rebind"} {
		t.Run(operation, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, requests := preparedRequestModeQualificationFixture(t, basic)
				finishColdGitOpsRuntime(t, basic, lease)
				source := reportSourceForRetirement(t, basic, lease)
				full := basic.(state.Store)
				old, err := full.DeploymentByID(t.Context(), requests[0].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				pending, err := full.CreateDeployment(t.Context(), state.Deployment{AppID: old.AppID, Scope: old.Scope,
					Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "registry.example/manual@sha256:" + strings.Repeat("f", 64)})
				if err != nil {
					t.Fatal(err)
				}
				if err := full.SetDeploymentRootfs(t.Context(), pending.ID, "/manual.ext4", "manual/"+pending.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := full.MarkDeploymentLive(t.Context(), pending.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("report mode released workload ownership before detach", err)
				}
				lifecycle := basic.(state.EnvironmentGitOpsLifecycleStore)
				if operation == "detach" {
					if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
						t.Fatal(err)
					}
					if _, err := basic.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug); !errors.Is(err, state.ErrNotFound) {
						t.Fatal("source is still active", err)
					}
				} else {
					spec := source.Spec
					spec.ManifestPath = "environments/rebound.yaml"
					next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
					if err != nil || next.ApprovedRevisionID != "" {
						t.Fatal("replacement inherited approval", err)
					}
				}

				if err := full.MarkDeploymentLive(t.Context(), old.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("detached held candidate gained manual activation authority", err)
				}
				if err := full.UpdateDeploymentStatus(t.Context(), old.ID, state.DeployLive, ""); !errors.Is(err, state.ErrInvalidStateTransition) {
					t.Fatal("detached candidate bypassed activation via status update", err)
				}
				if err := full.MarkDeploymentLive(t.Context(), pending.ID); err != nil {
					t.Fatal("detached history blocked a fresh manual deployment", err)
				}
				retained, err := full.DeploymentByID(t.Context(), old.ID)
				if err != nil || retained.EnvironmentWorkloadRuntime != old.EnvironmentWorkloadRuntime || !retained.EnvironmentWorkloadHeld() || retained.RootfsKey != old.RootfsKey {
					t.Fatal("manual handoff changed frozen history", err)
				}
			})
		})
	}
}

func TestGitOpsDetachAllowsActiveReleaseHandoff(t *testing.T) {
	for _, operation := range []string{"replace", "deactivate"} {
		t.Run(operation, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, requests := preparedRequestModeQualificationFixture(t, basic)
				finishColdGitOpsRuntime(t, basic, lease)
				var definition api.EnvironmentDefinition
				if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
					t.Fatal(err)
				}
				desired, err := environmentsync.Compile(definition)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
				qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
				for _, request := range requests {
					claim, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "ownership", time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					completeClaimedQualificationReceipts(t, basic.(requestQualificationReceiptStore), claim, qualificationPlacement(t, basic, 4096))
				}
				release, active, err := basic.(state.EnvironmentGitOpsGraphActivationStore).ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
				if err != nil || !active {
					t.Fatal("activate reviewed release", err)
				}
				source := reportSourceForRetirement(t, basic, lease)
				if err := basic.(state.EnvironmentGitOpsLifecycleStore).DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
					t.Fatal(err)
				}
				var members []state.ProjectReleaseMember
				for _, request := range requests {
					manual := manualOwnershipDeployment(t, basic.(state.Store), request.AppID, "production", false)
					members = append(members, state.ProjectReleaseMember{AppID: manual.AppID, DeploymentID: manual.ID})
				}
				publisher := basic.(state.ProjectReleaseSetPromotionStore)
				if operation == "replace" {
					next, err := publisher.PublishProjectReleaseSetIfActive(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug, release.ID, nil, 3600, members)
					if err != nil || !next.Active || next.ID == release.ID {
						t.Fatalf("detached release could not be replaced: %+v %v", next, err)
					}
				} else if err := publisher.DeactivateProjectReleaseSetIfActive(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug, release.ID, members); err != nil {
					t.Fatal("detached release could not be deactivated", err)
				}
			})
		})
	}
}

func TestPgGitOpsDetachedOwnershipGuardReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, requests := preparedRequestModeQualificationFixture(t, store)
	finishColdGitOpsRuntime(t, store, lease)
	source := reportSourceForRetirement(t, store, lease)
	manual, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: requests[0].AppID, Scope: "production",
		Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercentExplicit: true, ImageDigest: "registry.example/manual@sha256:" + strings.Repeat("f", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(t.Context(), manual.ID, "/manual.ext4", "manual/"+manual.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET status='live' WHERE id=$1", manual.ID); err == nil {
		t.Fatal("SQL promotion bypassed bound source ownership")
	}

	// Reinstall the guards over populated history and check reversible replay.
	body, err := migrations.FS.ReadFile("20261103190000001_gitops_detached_workload_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(body), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, script := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err := tx.Exec(t.Context(), script); err != nil {
			t.Fatal("populated guard replay", err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET status='live' WHERE id=$1", manual.ID); err != nil {
		t.Fatal("SQL guard retained detached ownership", err)
	}
	for _, statement := range []string{
		"UPDATE deployments SET status='live' WHERE id=$1",
		"UPDATE deployments SET environment_workload_held=false WHERE id=$1",
		"UPDATE deployments SET environment_workload_runtime=environment_workload_runtime || '{\"variables\":{\"tampered\":\"true\"}}'::jsonb WHERE id=$1",
	} {
		if _, err := pool.Exec(t.Context(), statement, requests[0].DeploymentID); err == nil {
			t.Fatal("detached frozen candidate lost its SQL protection", statement)
		}
	}
}

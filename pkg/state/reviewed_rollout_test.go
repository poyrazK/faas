package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemReviewedRollout(t *testing.T) { testReviewedRollout(t, state.NewMemStore()) }
func testReviewedRollout(t *testing.T, store reviewedStore) {
	for _, kind := range []state.DeploymentKind{state.DeploymentKindImage, state.DeploymentKindGitHub} {
		t.Run(string(kind), func(t *testing.T) { testReviewedRolloutKind(t, store, kind) })
	}
}
func testReviewedRolloutKind(t *testing.T, store reviewedStore, kind state.DeploymentKind) {
	t.Helper()
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, fmt.Sprintf("native-%s-%s@example.test", mode, kind), api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "native-project"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "native-" + mode + "-" + string(kind), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 3, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: mode}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
			if err != nil {
				t.Fatal(err)
			}
			review, err := store.ReadReleaseReview(ctx, app.ID, "staging")
			if err != nil {
				t.Fatal(err)
			}
			settings, err := state.WorkloadSettingsFromApp(app)
			if err != nil {
				t.Fatal(err)
			}
			request := state.Deployment{AppID: app.ID, Kind: kind, ImageDigest: "sha256:reviewed", Scope: "staging", ReviewedRolloutMode: mode, ExpectedReview: &review, ReviewedSettings: &settings}
			if mode == api.ExecutionModeService {
				request.RolloutState = "rolling_out"
			}
			explicit := request
			explicit.TrafficPercentExplicit = true
			if _, err := store.CreateDeployment(ctx, explicit); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("native rollout accepted explicit traffic: %v", err)
			}
			candidate, err := store.CreateDeployment(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.StagedReview == nil || candidate.StagedReview.WorkloadRevision != 1 || candidate.StagedReview.WorkloadConfigHash == "" {
				t.Fatal("native admission did not freeze named workload")
			}
			if candidate.ReviewedRolloutMode != "" {
				t.Fatal("admission metadata persisted")
			}
			if mode == api.ExecutionModeService && (candidate.RolloutState != "rolling_out" || candidate.TrafficPercent != 0) {
				t.Fatal("service activation bypassed readiness")
			}
			if mode == api.ExecutionModeWorker && candidate.TrafficPercent != 100 {
				t.Fatal("worker omitted traffic lost activation semantics")
			}
			if _, err := store.CreateDeployment(ctx, request); !errors.Is(err, state.ErrReleaseReviewChanged) {
				t.Fatalf("reused review created another generation: %v", err)
			}
			if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemReviewedServiceRecovery(t *testing.T) {
	testReviewedServiceRecovery(t, state.NewMemStore())
}
func testReviewedServiceRecovery(t *testing.T, store reviewedStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "reviewed-service-recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reviewed-service-recovery", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 3, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:base"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: api.DefaultEnvScope, RolloutState: "rolling_out", ReviewedRolloutMode: api.ExecutionModeService, ExpectedReview: &review})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	review, err = store.ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	recoverer := store.(state.ServiceRolloutBindingStore)
	if _, _, err := recoverer.RequestServiceRolloutAbort(state.WithServiceRecoveryReview(ctx, review), app.ID, candidate.ID, base.ID, "recover"); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("stale recovery wrote intent: %v", err)
	}
	observed, err := store.DeploymentByID(ctx, candidate.ID)
	if err != nil || observed.ServiceRolloutHandoff.Action != "" {
		t.Fatal("stale recovery changed handoff")
	}
	review, err = store.ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := recoverer.RequestServiceRolloutAbort(state.WithServiceRecoveryReview(ctx, review), app.ID, candidate.ID, base.ID, "recover")
	if err != nil || !accepted.ServiceRolloutHandoff.ActiveAbort() || accepted.ServiceRolloutHandoff.BindingsCheck == nil {
		t.Fatalf("missing reverse handoff: %+v %v", accepted, err)
	}
}

func TestMemReviewedPinnedServiceReadiness(t *testing.T) {
	testReviewedPinnedServiceReadiness(t, state.NewMemStore(), state.DefaultLocalNodeName)
}
func testReviewedPinnedServiceReadiness(t *testing.T, store reviewedStore, nodeID string) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "pinned-service-readiness@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "pinned-service"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "pinned-service", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 3, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", ImageDigest: "sha256:base", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}
	spec, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, acct.ID, app.ID, "staging", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", RolloutState: "rolling_out", ReviewedRolloutMode: api.ExecutionModeService, ExpectedReview: &review, ReviewedSettings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 256, nodeID, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	queued, err := store.BeginServiceRolloutCutover(ctx, candidate.ID)
	if !state.IsBindingReleaseRequired(err) || queued.ServiceRolloutHandoff.BindingsCheck == nil {
		t.Fatalf("missing native binding request: %v", err)
	}
	requestID := queued.ServiceRolloutHandoff.BindingsCheck.RequestID
	writeContext := func() context.Context {
		fence := bindingRecoveryFence(ctx, t, store.(state.BindingPromotionStore), acct, app, candidate)
		fence.PolicyRevision = 1
		fence.MaxVerificationAge = api.DefaultBindingVerificationAge
		return state.WithServiceRolloutBindingRequest(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), requestID)
	}
	if _, err := store.BeginServiceRolloutCutover(writeContext(), candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
		t.Fatalf("default desired=1 bypassed pinned desired=2: %v", err)
	}
	observed, err := store.DeploymentByID(ctx, base.ID)
	if err != nil || observed.TrafficPercent != 100 {
		t.Fatal("insufficient named readiness displaced predecessor")
	}
	// A later desired head must not change the already reviewed generation.
	settings.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 3, Desired: 3}
	if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 256, nodeID, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	routed, err := store.BeginServiceRolloutCutover(writeContext(), candidate.ID)
	if err != nil || routed.TrafficPercent != 100 {
		t.Fatalf("ready pinned desired=2 followed a changed head: %v", err)
	}
}

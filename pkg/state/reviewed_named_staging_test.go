package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestMemReviewedNamedStaging(t *testing.T) { testReviewedNamedStaging(t, state.NewMemStore()) }

func testReviewedNamedStaging(t *testing.T, store reviewedStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "named-review@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "reviewed-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "named-reviewed", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:base", Kind: state.DeploymentKindImage, Scope: "default", TrafficPercent: 100, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if review.EnvironmentID != env.ID || review.ProjectID != project.ID || review.WorkloadRevision != 0 || review.Scope != "staging" {
		t.Fatalf("unexpected review %+v", review)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	request := state.Deployment{AppID: app.ID, ImageDigest: "sha256:staged", Kind: state.DeploymentKindImage, Scope: "staging", TrafficPercent: 0, TrafficPercentExplicit: true, ExpectedReview: &review, ReviewedSettings: &settings}
	staged, err := store.CreateDeployment(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if staged.StagedReview == nil || staged.StagedReview.WorkloadRevision != 1 || staged.StagedReview.EnvironmentID != env.ID || staged.StagedReview.LatestDeploymentID != staged.ID {
		t.Fatal("committed workload review missing")
	}
	reader := store.(state.DeploymentWorkloadSpecReader)
	pinned, err := reader.ProjectEnvironmentWorkloadSpecForDeployment(ctx, account.ID, project.ID, staged.ID)
	if err != nil || pinned.Hash != staged.StagedReview.WorkloadConfigHash {
		t.Fatal("stage did not pin an immutable revision", err)
	}
	if err := store.MarkDeploymentLive(ctx, staged.ID); err != nil {
		t.Fatal(err)
	}
	traffic := store.(state.ReviewedTrafficStore)
	promoted, err := traffic.UpdateDeploymentTrafficReviewed(ctx, staged.ID, 100, "", *staged.StagedReview)
	if err != nil || promoted.TrafficPercent != 100 {
		t.Fatal("scoped promotion failed", err)
	}
	if current, err := store.DeploymentByID(ctx, base.ID); err != nil || current.TrafficPercent != 100 || current.Status != state.DeployLive {
		t.Fatal("staging changed default traffic")
	}
	// Editing desired settings leaves the deployed snapshot intact, and rejects
	// both stale insertion and a stale reviewed traffic change before any writes.
	fresh, err := store.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB = 512
	_, err = store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, pinned.Revision, settings)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedReview = &fresh
	if _, err := store.CreateDeployment(ctx, request); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("stale stage allowed: %v", err)
	}
	if _, err := traffic.UpdateDeploymentTrafficReviewed(ctx, staged.ID, 100, "", fresh); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("stale promotion allowed: %v", err)
	}
	after, err := reader.ProjectEnvironmentWorkloadSpecForDeployment(ctx, account.ID, project.ID, staged.ID)
	if err != nil || after.ID != pinned.ID || after.Settings.RAMMB != 256 {
		t.Fatal("edit changed frozen deployment settings")
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	protected, err := store.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil || !protected.Protected {
		t.Fatal("protected registry state missing")
	}
	if _, err := traffic.UpdateDeploymentTrafficReviewed(ctx, staged.ID, 100, "", protected); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatal("manual reviewed promotion bypassed protected target")
	}
	wrong := protected
	wrong.EnvironmentID = "replacement"
	request.ExpectedReview = &wrong
	if _, err := store.CreateDeployment(ctx, request); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatal("environment identity mismatch allowed")
	}
}

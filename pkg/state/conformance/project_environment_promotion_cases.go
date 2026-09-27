package conformance

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testProjectEnvironmentPromotionReleaseGraphCheckpoints(t *testing.T, fx *Fixture) {
	publisher, ok := fx.Store.(state.ProjectReleaseSetStore)
	if !ok {
		t.Fatal("store does not support project release sets")
	}
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{
		AccountID: fx.Account.ID, Slug: "promotion-graph-" + uuid.NewString()[:8],
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := fx.Store.CreateProjectEnvironment(fx.Ctx, state.ProjectEnvironment{
		AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatalf("CreateProjectEnvironment(staging): %v", err)
	}
	appSlug := "promotion-api-" + uuid.NewString()[:8]
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, ProjectID: project.ID, Slug: appSlug,
		WorkloadName: "api", Status: state.AppActive,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600},
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	createLive := func(scope string) state.Deployment {
		t.Helper()
		deployment, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:promotion-" + scope, Status: state.DeployPending,
			Scope: scope, TrafficPercent: 100,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%s): %v", scope, err)
		}
		if err := fx.Store.MarkDeploymentLive(fx.Ctx, deployment.ID); err != nil {
			t.Fatalf("MarkDeploymentLive(%s): %v", scope, err)
		}
		return deployment
	}
	staging := createLive("staging")
	production := createLive("production")
	source, err := publisher.PublishProjectReleaseSet(fx.Ctx, fx.Account.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: staging.ID}})
	if err != nil {
		t.Fatalf("PublishProjectReleaseSet(staging): %v", err)
	}
	previous, err := publisher.PublishProjectReleaseSet(fx.Ctx, fx.Account.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: production.ID}})
	if err != nil {
		t.Fatalf("PublishProjectReleaseSet(production previous): %v", err)
	}
	target, err := publisher.PublishProjectReleaseSet(fx.Ctx, fx.Account.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: production.ID}})
	if err != nil {
		t.Fatalf("PublishProjectReleaseSet(production target): %v", err)
	}
	promotion, _, err := fx.Store.CreateProjectEnvironmentPromotion(fx.Ctx, state.ProjectEnvironmentPromotion{
		AccountID: fx.Account.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "staging", ToEnvironment: "production",
		PromotionHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdempotencyKey: "promotion-graph-" + uuid.NewString(), Status: "running",
		VerificationStatus: "pending", ReleaseGraphMode: true,
		SourceReleaseSetID: source.ID, PreviousTargetReleaseSetID: previous.ID, ReleaseTTLSeconds: 1800,
	}, nil)
	if err != nil {
		t.Fatalf("CreateProjectEnvironmentPromotion: %v", err)
	}
	checkpoint, err := fx.Store.UpdateProjectEnvironmentPromotionReleaseSets(fx.Ctx, fx.Account.ID, promotion.ID, target.ID, "")
	if err != nil {
		t.Fatalf("UpdateProjectEnvironmentPromotionReleaseSets(target): %v", err)
	}
	if checkpoint.TargetReleaseSetID != target.ID || checkpoint.RestoredTargetReleaseSetID != "" {
		t.Fatalf("target graph checkpoint = %+v", checkpoint)
	}
	checkpoint, err = fx.Store.UpdateProjectEnvironmentPromotionReleaseSets(fx.Ctx, fx.Account.ID, promotion.ID, "", previous.ID)
	if err != nil {
		t.Fatalf("UpdateProjectEnvironmentPromotionReleaseSets(restored): %v", err)
	}
	if checkpoint.TargetReleaseSetID != target.ID || checkpoint.RestoredTargetReleaseSetID != previous.ID {
		t.Fatalf("restored graph checkpoint = %+v", checkpoint)
	}
	got, _, err := fx.Store.ProjectEnvironmentPromotionByID(fx.Ctx, fx.Account.ID, project.Slug, "production", promotion.ID)
	if err != nil {
		t.Fatalf("ProjectEnvironmentPromotionByID: %v", err)
	}
	if !got.ReleaseGraphMode || got.SourceReleaseSetID != source.ID || got.PreviousTargetReleaseSetID != previous.ID ||
		got.TargetReleaseSetID != target.ID || got.RestoredTargetReleaseSetID != previous.ID || got.ReleaseTTLSeconds != 1800 {
		t.Fatalf("persisted promotion release graph checkpoints = %+v", got)
	}
}

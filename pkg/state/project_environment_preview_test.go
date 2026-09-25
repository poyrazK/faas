package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectEnvironmentPreviewLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-lifecycle@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "preview-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	environment, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-381",
		PreviewPRNumber: 381, PreviewHeadSHA: shaA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewState != ProjectEnvironmentPreviewOpen || environment.PreviewExpiresAt == nil ||
		!environment.PreviewExpiresAt.After(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("new preview lifecycle = state %q expiry %v", environment.PreviewState, environment.PreviewExpiresAt)
	}

	reopenedUntil := time.Now().UTC().Add(48 * time.Hour)
	environment, err = store.UpdateProjectEnvironmentPreviewHead(ctx, account.ID, project.ID, 381, shaB, reopenedUntil)
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewState != ProjectEnvironmentPreviewOpen || environment.PreviewHeadSHA != shaB ||
		environment.PreviewExpiresAt == nil || !environment.PreviewExpiresAt.Equal(reopenedUntil) {
		t.Fatalf("updated preview lifecycle = %+v", environment)
	}

	closedUntil := time.Now().UTC().Add(ProjectEnvironmentPreviewCloseGrace)
	environment, err = store.CloseProjectEnvironmentPreview(ctx, account.ID, project.ID, 381, closedUntil)
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewState != ProjectEnvironmentPreviewClosed || environment.PreviewExpiresAt == nil ||
		!environment.PreviewExpiresAt.Equal(closedUntil) {
		t.Fatalf("closed preview lifecycle = %+v", environment)
	}
	repeatedCloseUntil := time.Now().UTC().Add(2 * ProjectEnvironmentPreviewCloseGrace)
	environment, err = store.CloseProjectEnvironmentPreview(ctx, account.ID, project.ID, 381, repeatedCloseUntil)
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewExpiresAt == nil || !environment.PreviewExpiresAt.Equal(closedUntil) {
		t.Fatalf("repeated close extended preview grace: got %v, want %v", environment.PreviewExpiresAt, closedUntil)
	}

	store.mu.Lock()
	expiredAt := time.Now().UTC().Add(-time.Minute)
	environment.PreviewExpiresAt = &expiredAt
	store.projectEnvironments[environment.ID] = environment
	store.mu.Unlock()

	candidates, err := store.ListProjectEnvironmentPreviewsForTeardown(ctx, time.Now().UTC(), 10)
	if err != nil || len(candidates) != 1 || candidates[0].ID != environment.ID {
		t.Fatalf("expired preview candidates = %+v, err=%v", candidates, err)
	}
	drainUntil := time.Now().UTC().Add(ProjectEnvironmentPreviewDrainGrace)
	environment, deployments, err := store.BeginProjectEnvironmentPreviewTeardown(
		ctx, account.ID, project.ID, environment.Slug, time.Now().UTC(), drainUntil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewState != ProjectEnvironmentPreviewTearingDown || environment.PreviewExpiresAt == nil ||
		!environment.PreviewExpiresAt.Equal(drainUntil) || len(deployments) != 0 {
		t.Fatalf("teardown result = environment %+v deployments %+v", environment, deployments)
	}
	if _, err := store.UpdateProjectEnvironmentPreviewHead(ctx, account.ID, project.ID, 381, shaA, time.Now().Add(24*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("reopening a tearing-down preview err = %v, want ErrConflict", err)
	}
	if _, err := store.CloseProjectEnvironmentPreview(ctx, account.ID, project.ID, 381, time.Now().Add(24*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("closing a tearing-down preview err = %v, want ErrConflict", err)
	}
}

func TestProjectEnvironmentPreviewTeardownDrainsLiveReleases(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-drain@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "preview-drain"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-382",
		PreviewPRNumber: 382, PreviewHeadSHA: "cccccccccccccccccccccccccccccccccccccccc",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment := Deployment{
		ID: "preview-release", AppID: app.ID, Scope: environment.Slug,
		ImageDigest: "sha256:preview", Status: DeployLive, TrafficPercent: 100,
	}
	store.mu.Lock()
	store.deployments[deployment.ID] = deployment
	expiredAt := time.Now().UTC().Add(-time.Minute)
	environment.PreviewExpiresAt = &expiredAt
	store.projectEnvironments[environment.ID] = environment
	store.mu.Unlock()

	now := time.Now().UTC()
	_, drained, err := store.BeginProjectEnvironmentPreviewTeardown(
		ctx, account.ID, project.ID, environment.Slug, now, now.Add(ProjectEnvironmentPreviewDrainGrace),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(drained) != 1 || drained[0].DeploymentID != deployment.ID || drained[0].AppID != app.ID {
		t.Fatalf("drained releases = %+v", drained)
	}
	got, err := store.DeploymentByID(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeploySuperseded || got.TrafficPercent != 0 {
		t.Fatalf("release after teardown = status %q traffic %d", got.Status, got.TrafficPercent)
	}

	lateBuild := Deployment{
		ID: "preview-late-build", AppID: app.ID, Scope: environment.Slug,
		ImageDigest: "sha256:preview-late", Status: DeploySnapshotting, TrafficPercent: 100,
	}
	store.mu.Lock()
	store.deployments[lateBuild.ID] = lateBuild
	store.mu.Unlock()
	if err := store.MarkDeploymentLive(ctx, lateBuild.ID); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("late release promotion err = %v, want ErrInvalidStateTransition", err)
	}
	lateBuild, err = store.DeploymentByID(ctx, lateBuild.ID)
	if err != nil || lateBuild.Status != DeploySuperseded || lateBuild.TrafficPercent != 0 {
		t.Fatalf("late release after promotion guard = %+v err=%v", lateBuild, err)
	}

	lateLive := Deployment{
		ID: "preview-late-live", AppID: app.ID, Scope: environment.Slug,
		ImageDigest: "sha256:preview-late-live", Status: DeployLive, TrafficPercent: 100,
	}
	store.mu.Lock()
	store.deployments[lateLive.ID] = lateLive
	store.mu.Unlock()
	nextDrainAt := now.Add(2 * ProjectEnvironmentPreviewDrainGrace)
	redraining, lateDeployments, err := store.BeginProjectEnvironmentPreviewTeardown(
		ctx, account.ID, project.ID, environment.Slug, nextDrainAt, nextDrainAt.Add(ProjectEnvironmentPreviewDrainGrace),
	)
	if err != nil || len(lateDeployments) != 1 || lateDeployments[0].DeploymentID != lateLive.ID ||
		redraining.PreviewExpiresAt == nil || !redraining.PreviewExpiresAt.After(nextDrainAt) {
		t.Fatalf("late live release drain = environment %+v releases %+v err=%v", redraining, lateDeployments, err)
	}
}

//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentPreviewLifecycle(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-lifecycle@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-381",
		PreviewPRNumber: 381, PreviewHeadSHA: shaA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.PreviewState != state.ProjectEnvironmentPreviewOpen || environment.PreviewExpiresAt == nil {
		t.Fatalf("new preview lifecycle = %+v", environment)
	}

	openedUntil := time.Now().UTC().Add(48 * time.Hour)
	environment, err = store.UpdateProjectEnvironmentPreviewHead(ctx, account.ID, project.ID, 381, shaB, openedUntil)
	if err != nil || environment.PreviewState != state.ProjectEnvironmentPreviewOpen || environment.PreviewHeadSHA != shaB {
		t.Fatalf("updated preview = %+v err=%v", environment, err)
	}
	// PostgreSQL timestamps have microsecond precision; normalize the expected
	// value so this persistence assertion does not compare against discarded
	// nanoseconds from time.Now().
	closedUntil := time.Now().UTC().Add(state.ProjectEnvironmentPreviewCloseGrace).Truncate(time.Microsecond)
	environment, err = store.CloseProjectEnvironmentPreview(ctx, account.ID, project.ID, 381, closedUntil)
	if err != nil || environment.PreviewState != state.ProjectEnvironmentPreviewClosed ||
		environment.PreviewExpiresAt == nil || !environment.PreviewExpiresAt.Equal(closedUntil) {
		t.Fatalf("closed preview = %+v err=%v", environment, err)
	}

	now := closedUntil.Add(time.Second)
	candidates, err := store.ListProjectEnvironmentPreviewsForTeardown(ctx, now, 10)
	if err != nil || len(candidates) != 1 || candidates[0].ID != environment.ID {
		t.Fatalf("expired preview candidates = %+v err=%v", candidates, err)
	}
	drainUntil := now.Add(state.ProjectEnvironmentPreviewDrainGrace)
	environment, deployments, err := store.BeginProjectEnvironmentPreviewTeardown(
		ctx, account.ID, project.ID, environment.Slug, now, drainUntil,
	)
	if err != nil || environment.PreviewState != state.ProjectEnvironmentPreviewTearingDown ||
		environment.PreviewExpiresAt == nil || !environment.PreviewExpiresAt.Equal(drainUntil) || len(deployments) != 0 {
		t.Fatalf("teardown = environment %+v deployments %+v err=%v", environment, deployments, err)
	}
	got, err := store.ProjectEnvironmentByPreviewPR(ctx, account.ID, project.ID, 381)
	if err != nil || got.PreviewState != state.ProjectEnvironmentPreviewTearingDown {
		t.Fatalf("preview lookup after teardown = %+v err=%v", got, err)
	}
}

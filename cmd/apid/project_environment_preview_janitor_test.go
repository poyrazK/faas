package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentPreviewJanitorDrainsBeforeDelete(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-janitor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-janitor"})
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-381",
		PreviewPRNumber: 381, PreviewHeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PreviewState: state.ProjectEnvironmentPreviewOpen, PreviewExpiresAt: &expiredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, nil, "", nil)
	lifecycleStore := state.ProjectEnvironmentPreviewLifecycleStore(store)
	now := time.Now().UTC()
	if err := srv.sweepProjectEnvironmentPreviews(ctx, lifecycleStore, now); err != nil {
		t.Fatal(err)
	}
	environment, err = store.ProjectEnvironmentByPreviewPR(ctx, account.ID, project.ID, 381)
	if err != nil || environment.PreviewState != state.ProjectEnvironmentPreviewTearingDown {
		t.Fatalf("preview after drain start = %+v err=%v", environment, err)
	}

	if err := srv.sweepProjectEnvironmentPreviews(ctx, lifecycleStore, now.Add(state.ProjectEnvironmentPreviewDrainGrace+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentByPreviewPR(ctx, account.ID, project.ID, 381); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("preview after drain grace err = %v, want ErrNotFound", err)
	}
}

package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcileGitHubProjectPreviewEnvironmentLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "project-preview-webhook@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{
		AccountID: acct.ID, Slug: "preview-webhook", RepoFullName: "acme/api", InstallID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := store.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.PreviewTTLHours = 48
	policy.PreviewEnvironmentFrom = "staging"
	if _, err := store.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, nil, "gregale.dev", nil)
	request := githubdProjectPreviewRequest{
		AccountID: acct.ID, InstallationID: 42, RepoFullName: "acme/api", PRNumber: 381,
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Action: "opened",
	}

	first, err := srv.ReconcileGitHubProjectPreviewEnvironment(ctx, request)
	if err != nil {
		t.Fatalf("open PR: %v", err)
	}
	if !first.Reconciled || first.Environment.Slug != "pr-381" || first.Environment.PreviewHeadSHA != request.HeadSHA || first.Environment.PreviewState != state.ProjectEnvironmentPreviewOpen {
		t.Fatalf("opened preview = %+v", first)
	}
	if first.Environment.PreviewExpiresAt == nil || !first.Environment.PreviewExpiresAt.After(time.Now().Add(47*time.Hour)) {
		t.Fatalf("open preview expiry = %v, want configured 48-hour lease", first.Environment.PreviewExpiresAt)
	}

	request.Action = "synchronize"
	request.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second, err := srv.ReconcileGitHubProjectPreviewEnvironment(ctx, request)
	if err != nil {
		t.Fatalf("synchronize PR: %v", err)
	}
	if second.Environment.ID != first.Environment.ID || second.Environment.PreviewHeadSHA != request.HeadSHA {
		t.Fatalf("synchronized preview = %+v, want existing ID %q and new SHA", second.Environment, first.Environment.ID)
	}

	request.Action = "closed"
	request.HeadSHA = ""
	closed, err := srv.ReconcileGitHubProjectPreviewEnvironment(ctx, request)
	if err != nil {
		t.Fatalf("close PR: %v", err)
	}
	if !closed.Reconciled || closed.Environment.PreviewState != state.ProjectEnvironmentPreviewClosed || closed.Environment.PreviewExpiresAt == nil {
		t.Fatalf("closed preview = %+v", closed)
	}
	if closed.Environment.PreviewExpiresAt.After(time.Now().Add(state.ProjectEnvironmentPreviewCloseGrace + time.Minute)) {
		t.Fatalf("closed preview expiry = %v, exceeds close grace", closed.Environment.PreviewExpiresAt)
	}

	closedAgain, err := srv.ReconcileGitHubProjectPreviewEnvironment(ctx, request)
	if err != nil {
		t.Fatalf("repeat close PR: %v", err)
	}
	if !closedAgain.Environment.PreviewExpiresAt.Equal(*closed.Environment.PreviewExpiresAt) {
		t.Fatalf("duplicate close extended grace: first=%v second=%v", closed.Environment.PreviewExpiresAt, closedAgain.Environment.PreviewExpiresAt)
	}
}

func TestReconcileGitHubProjectPreviewEnvironmentRequiresProjectBinding(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "project-preview-optout@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{
		AccountID: acct.ID, Slug: "preview-optout", RepoFullName: "acme/api", InstallID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, nil, "gregale.dev", nil)
	result, err := srv.ReconcileGitHubProjectPreviewEnvironment(ctx, githubdProjectPreviewRequest{
		AccountID: acct.ID, InstallationID: 42, RepoFullName: "acme/api", PRNumber: 17,
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Action: "opened",
	})
	if err != nil || result.Reconciled {
		t.Fatalf("unconfigured preview = %+v, %v; want no-op", result, err)
	}
	if _, err := store.ProjectEnvironmentByPreviewPR(ctx, acct.ID, project.ID, 17); err == nil {
		t.Fatal("unconfigured project unexpectedly received a preview environment")
	}
}

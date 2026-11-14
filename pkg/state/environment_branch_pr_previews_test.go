package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemEnvironmentBranchPRPreviews(t *testing.T) {
	environmentBranchPRPreviews(t, state.NewMemStore())
}

func TestPgEnvironmentBranchPRPreviews(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchPRPreviews(t, s)
}

// ADR-20261113121342: a project's GitHub policy stores its preview mode, app
// by default, and a branch made for a pull request records its number.
func environmentBranchPRPreviews(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "branch-pr-previews@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "branch-previews"})
	if err != nil {
		t.Fatal(err)
	}
	policies, ok := s.(state.GitHubDeployPolicyStore)
	if !ok {
		t.Fatal("store has no GitHub deployment policies")
	}
	defaults, err := policies.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil || defaults.PreviewMode != state.PreviewModeApp {
		t.Fatalf("default policy = %+v, %v; want preview mode app", defaults, err)
	}
	unset := defaults
	unset.PreviewMode = ""
	if stored, err := policies.UpsertGitHubDeployPolicy(ctx, unset); err != nil || stored.PreviewMode != state.PreviewModeApp {
		t.Fatalf("unset mode stored as %+v, %v; want app", stored, err)
	}
	branch := defaults
	branch.PreviewMode = state.PreviewModeBranch
	if stored, err := policies.UpsertGitHubDeployPolicy(ctx, branch); err != nil || stored.PreviewMode != state.PreviewModeBranch {
		t.Fatalf("branch mode stored as %+v, %v", stored, err)
	}
	if got, err := policies.GetGitHubDeployPolicy(ctx, project.ID, acct.ID); err != nil || got.PreviewMode != state.PreviewModeBranch {
		t.Fatalf("branch mode read back as %+v, %v", got, err)
	}
	invalid := defaults
	invalid.PreviewMode = "fork"
	if _, err := policies.UpsertGitHubDeployPolicy(ctx, invalid); err == nil {
		t.Fatal("an unknown preview mode was stored")
	}

	create := func(slug string, pullRequest int) (state.ProjectEnvironmentBranch, error) {
		_, created, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: slug, PullRequestNumber: pullRequest,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
		}, api.MustLimitsFor(acct.Plan))
		return created, err
	}
	if created, err := create("pr-7", 7); err != nil || created.PullRequestNumber != 7 {
		t.Fatalf("pull request branch = %+v, %v; want pull request 7", created, err)
	}
	if stored, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-7"); err != nil || stored.PullRequestNumber != 7 {
		t.Fatalf("stored pull request branch = %+v, %v; want pull request 7", stored, err)
	}
	if created, err := create("by-hand", 0); err != nil || created.PullRequestNumber != 0 {
		t.Fatalf("hand-made branch = %+v, %v; want no pull request", created, err)
	}
	if stored, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "by-hand"); err != nil || stored.PullRequestNumber != 0 {
		t.Fatalf("stored hand-made branch = %+v, %v; want no pull request", stored, err)
	}
	if _, err := create("pr-bad", -1); err == nil {
		t.Fatal("a negative pull request number was accepted")
	}
}

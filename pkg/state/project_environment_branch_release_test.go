package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 amendment 2026-11-19: a branch deployment does not
// carry its parent's release command. The parent's release already ran
// against the data the branch copies, and schedd primes a deployment with a
// release command only after a release task for it succeeds, which nothing
// starts for a branch: the branch's URL answered 404 forever.
func TestMemEnvironmentBranchDeploymentSkipsTheParentRelease(t *testing.T) {
	environmentBranchDeploymentSkipsTheParentRelease(t, state.NewMemStore())
}

func environmentBranchDeploymentSkipsTheParentRelease(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "branch-release@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rel"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:rel",
		ReleaseCommand: []string{"./migrate", "up"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(parent.ReleaseCommand) == 0 {
		t.Fatalf("the parent kept no release command: %+v", parent)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/rel.ext4", "layers/rel.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-3",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}
	branch, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-3", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(branch.ReleaseCommand) != 0 || branch.ReleaseCommandShell {
		t.Fatalf("branch deployment = %+v; want no release command", branch)
	}
	stored, err := s.DeploymentByID(ctx, branch.ID)
	if err != nil || len(stored.ReleaseCommand) != 0 {
		t.Fatalf("stored branch deployment = %+v, %v", stored, err)
	}
}

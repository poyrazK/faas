package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635: a branch copies the parent's configuration in one
// transaction, reuses the parent's built artifact for each live workload, and
// is retired with its deployments.
func TestMemProjectEnvironmentBranchLifecycle(t *testing.T) {
	projectEnvironmentBranchLifecycle(t, state.NewMemStore())
}

func projectEnvironmentBranchLifecycle(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "branches@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "branched"})
	if err != nil {
		t.Fatal(err)
	}
	served, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, MinInstances: 1})
	if err != nil {
		t.Fatal(err)
	}
	// An app the parent never deployed has nothing for a branch to reuse.
	if _, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "idle", WorkloadName: "idle", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	parentDeployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: served.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:branched"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parentDeployment.ID, "/branched.ext4", "layers/branched.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parentDeployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, acct.ID, served.ID, "production", "GREETING", "hello"); err != nil {
		t.Fatal(err)
	}

	request := func(parent, slug string) state.ProjectEnvironmentBranchCreate {
		return state.ProjectEnvironmentBranchCreate{AccountID: acct.ID, ProjectID: project.ID, ParentSlug: parent, Slug: slug,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
			MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches}
	}
	env, branch, result, err := s.CreateProjectEnvironmentBranch(ctx, request("production", "pr-1"), limits)
	if err != nil {
		t.Fatal(err)
	}
	if env.Slug != "pr-1" || branch.EnvironmentID != env.ID || branch.ParentSlug != "production" || branch.Depth != 1 ||
		branch.EgressMode != state.EnvironmentBranchEgressLive || branch.BranchPoint.IsZero() || !branch.ExpiresAt.After(branch.CreatedAt) {
		t.Fatalf("branch = %+v env = %+v", branch, env)
	}
	if result.VariablesCopied != 1 {
		t.Fatalf("variables copied = %d, want 1", result.VariablesCopied)
	}
	// The parent keeps one warm instance; a branch boots only what is called.
	if spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "pr-1", served.ID); err != nil || spec.Settings.MinInstances != 0 {
		t.Fatalf("branch workload spec = %+v, %v; want no warm floor", spec.Settings, err)
	}
	if got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-1"); err != nil || got.EnvironmentID != env.ID || got.ParentSlug != "production" {
		t.Fatalf("branch by slug = %+v, %v", got, err)
	}
	if _, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "production"); !errors.Is(err, state.ErrProjectEnvironmentNotBranch) || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production as branch: %v", err)
	}
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, request("production", "pr-1"), limits); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate branch: %v", err)
	}
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, request("missing", "pr-9"), limits); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}

	workloads, err := s.ProjectEnvironmentBranchWorkloads(ctx, acct.ID, project.ID, "pr-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 1 || workloads[0].AppID != served.ID || workloads[0].WorkloadSlug != "web" || workloads[0].SourceDeploymentID != parentDeployment.ID {
		t.Fatalf("workloads = %+v", workloads)
	}
	deployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", served.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ID == parentDeployment.ID || deployment.Scope != "pr-1" || deployment.Status != state.DeploySnapshotting ||
		deployment.RootfsKey != "layers/branched.ext4" || deployment.ImageDigest != "sha256:branched" || deployment.TrafficPercent != 100 ||
		deployment.Reason != state.EnvironmentBranchDeploymentReason {
		t.Fatalf("branch deployment = %+v", deployment)
	}
	again, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", served.ID)
	if err != nil || again.ID != deployment.ID {
		t.Fatalf("second create = %s, %v; want %s", again.ID, err, deployment.ID)
	}
	if lazy, err := s.EnvironmentBranchDeployment(ctx, deployment.ID); err != nil || !lazy {
		t.Fatalf("branch deployment lazy = %v, %v", lazy, err)
	}
	if lazy, err := s.EnvironmentBranchDeployment(ctx, parentDeployment.ID); err != nil || lazy {
		t.Fatalf("parent deployment lazy = %v, %v", lazy, err)
	}
	// The activation imaged applies once schedd reports readiness.
	if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}

	child, _, _, err := s.CreateProjectEnvironmentBranch(ctx, request("pr-1", "pr-1-a"), limits)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, child.Slug); err != nil || got.Depth != 2 || got.ParentSlug != "pr-1" {
		t.Fatalf("child = %+v, %v", got, err)
	}
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, request("pr-1-a", "pr-1-a-b"), limits); !errors.Is(err, state.ErrProjectEnvironmentBranchDepth) {
		t.Fatalf("depth 3: %v", err)
	}
	capped := request("production", "pr-2")
	capped.MaxBranches = 2
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, capped, limits); !errors.Is(err, state.ErrProjectEnvironmentBranchLimit) {
		t.Fatalf("account cap: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-2"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused branch left an environment: %v", err)
	}

	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1"); !errors.Is(err, state.ErrProjectEnvironmentBranchChildren) {
		t.Fatalf("delete parent branch: %v", err)
	}
	if err := s.DeleteProjectEnvironment(ctx, acct.ID, project.ID, "pr-1"); !errors.Is(err, state.ErrProjectEnvironmentBranchChildren) {
		t.Fatalf("plain delete of parent branch: %v", err)
	}
	expired, err := s.ExpiredProjectEnvironmentBranches(ctx, time.Now().Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Slug != "pr-1-a" {
		t.Fatalf("expired = %+v, want only the leaf", expired)
	}
	if listed, err := s.ListProjectEnvironmentBranches(ctx, acct.ID, project.ID); err != nil || len(listed) != 2 || listed[0].Slug != "pr-1" || listed[1].Slug != "pr-1-a" {
		t.Fatalf("listed = %+v, %v", listed, err)
	}

	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted branch environment: %v", err)
	}
	retired, err := s.DeploymentByID(ctx, deployment.ID)
	if err != nil || retired.Status != state.DeploySuperseded || retired.TrafficPercent != 0 {
		t.Fatalf("retired branch deployment = %+v, %v", retired, err)
	}
	parent, err := s.DeploymentByID(ctx, parentDeployment.ID)
	if err != nil || parent.Status != state.DeployLive || parent.TrafficPercent != 100 {
		t.Fatalf("parent deployment after branch delete = %+v, %v", parent, err)
	}
	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production is not a branch: %v", err)
	}
}

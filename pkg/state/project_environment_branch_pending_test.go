package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 2: a parent that binds managed PostgreSQL branches
// with its data pending. The credential is not copied, the workload waits for
// the reconciler, and a permanent failure stops the attempts.
func TestMemProjectEnvironmentBranchWithManagedPostgresStartsPending(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "branch-pending@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:pending"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/pending.ext4", "layers/pending.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed"), ManagedPostgresBindingID: "binding-1", ManagedCredentialRef: "ref-1", ManagedCredentialGeneration: 1,
		ManagedPostgresAccess: "read_write"}); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	_, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: 2,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if branch.DataState != state.EnvironmentBranchDataPending {
		t.Fatalf("branch data = %q; want pending", branch.DataState)
	}
	if _, err := s.GetAppSecretInScope(ctx, acct.ID, app.ID, "pr-1", "DATABASE_URL"); err == nil {
		t.Fatal("the parent's managed credential was copied into the branch")
	}
	deployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, deployment.ID); err != nil || ready {
		t.Fatalf("pending data ready = %v, %v", ready, err)
	}
	lease := uuid.NewString()
	claimed, err := s.ClaimProjectEnvironmentBranchData(ctx, lease, time.Now(), time.Minute)
	if err != nil || claimed.EnvironmentID != branch.EnvironmentID || claimed.DataLeaseToken != lease {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if err := s.RetryProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, time.Now(), "quota", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-1")
	if err != nil || got.DataState != state.EnvironmentBranchDataFailed || got.DataError != "quota" {
		t.Fatalf("failed branch = %+v, %v", got, err)
	}
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, uuid.NewString(), time.Now().Add(time.Hour), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed branch claimed again: %v", err)
	}
}

// A pull request's build into a branch whose data is pending waits like the
// branch's own deployments (ADR-20261113121342): it is not data-ready, the
// reconciler primes it once the data is, unless it has a release command,
// which imaged resumes. Reopening a ready branch's data makes it pending
// again with fresh attempts.
func TestMemProjectEnvironmentBranchScopeBuildsWaitForData(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "branch-scope@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "scope"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:scope"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/scope.ext4", "layers/scope.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed"), ManagedPostgresBindingID: "binding-1", ManagedCredentialRef: "ref-1", ManagedCredentialGeneration: 1,
		ManagedPostgresAccess: "read_write"}); err != nil {
		t.Fatal(err)
	}
	_, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1", PullRequestNumber: 1,
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: 2,
	}, api.MustLimitsFor(acct.Plan))
	if err != nil || branch.DataState != state.EnvironmentBranchDataPending {
		t.Fatalf("branch = %+v, %v; want data pending", branch, err)
	}
	build := func(release []string) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "pr-1", Kind: state.DeploymentKindPreview, ImageDigest: "sha256:pr", ReleaseCommand: release})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
		return d
	}
	plain, migrating := build(nil), build([]string{"migrate"})
	production, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []state.Deployment{plain, migrating} {
		if ready, err := s.EnvironmentBranchDataReady(ctx, d.ID); err != nil || ready {
			t.Fatalf("build %s into the pending branch: data ready = %v, %v; want false", d.ID, ready, err)
		}
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, production.ID); err != nil || !ready {
		t.Fatalf("production deployment data ready = %v, %v; want true", ready, err)
	}
	awaiting, err := s.ProjectEnvironmentBranchDeploymentsAwaitingActivation(ctx, branch.EnvironmentID)
	if err != nil || len(awaiting) != 1 || awaiting[0].ID != plain.ID {
		t.Fatalf("awaiting activation = %+v, %v; want only the build without a release command", awaiting, err)
	}
	// Once its release command succeeded (imaged held it until the data was
	// ready), a migrating build is primed on activation too.
	if err := s.SetDeploymentRootfs(ctx, migrating.ID, "/migrating.ext4", "layers/migrating.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: migrating.ID,
		Kind: state.AppTaskKindRelease, Command: []string{"migrate"}, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextAppTask(ctx, "test", time.Now(), time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim release task = %+v, %v", claimed, err)
	}
	if _, err := s.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: new(int), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	awaiting, err = s.ProjectEnvironmentBranchDeploymentsAwaitingActivation(ctx, branch.EnvironmentID)
	if err != nil || len(awaiting) != 2 {
		t.Fatalf("awaiting activation after the release command = %+v, %v; want both builds", awaiting, err)
	}

	lease := uuid.NewString()
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, lease, time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, time.Now(), "provider", false); err != nil {
		t.Fatal(err)
	}
	if reopened, err := s.ReopenProjectEnvironmentBranchData(ctx, branch.EnvironmentID, time.Now()); err != nil || reopened {
		t.Fatalf("reopen pending data = %v, %v; want false", reopened, err)
	}
	lease = uuid.NewString()
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, lease, time.Now().Add(time.Minute), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, plain.ID); err != nil || !ready {
		t.Fatalf("build after the data is ready: data ready = %v, %v; want true", ready, err)
	}
	now := time.Now()
	if reopened, err := s.ReopenProjectEnvironmentBranchData(ctx, branch.EnvironmentID, now); err != nil || !reopened {
		t.Fatalf("reopen ready data = %v, %v; want true", reopened, err)
	}
	got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-1")
	if err != nil || got.DataState != state.EnvironmentBranchDataPending || got.DataAttempts != 0 || got.DataError != "" {
		t.Fatalf("reopened branch = %+v, %v; want pending with fresh attempts", got, err)
	}
	lease = uuid.NewString()
	if claimed, err := s.ClaimProjectEnvironmentBranchData(ctx, lease, now, time.Minute); err != nil || claimed.EnvironmentID != branch.EnvironmentID {
		t.Fatalf("claim reopened data = %+v, %v; want it due at once", claimed, err)
	}
	// Failed data does not hold a build into the branch's scope: the
	// branch's own deployments stay dark, a pull request build serves.
	if err := s.RetryProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, now, "gave up", true); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, plain.ID); err != nil || !ready {
		t.Fatalf("build into a branch whose data failed: data ready = %v, %v; want true", ready, err)
	}
}

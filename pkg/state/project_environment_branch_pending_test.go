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

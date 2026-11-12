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

func TestMemProjectEnvironmentBranchExtend(t *testing.T) {
	projectEnvironmentBranchExtend(t, state.NewMemStore())
}

// A branch's expiry moves; a branch whose data is still being copied, an
// environment that is not a branch and an expiry before the branch point are
// refused.
func projectEnvironmentBranchExtend(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	extender, ok := any(s).(state.ProjectEnvironmentBranchExtender)
	if !ok {
		t.Fatal("the store cannot extend environment branches")
	}
	acct, err := s.CreateAccount(ctx, "branch-extend@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "extended"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:extended"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/extended.ext4", "layers/extended.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	_, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Microsecond)
	extended, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", later)
	if err != nil || !extended.ExpiresAt.Equal(later) || extended.EnvironmentID != branch.EnvironmentID {
		t.Fatalf("extended = %+v, %v; want expiry %s", extended, err, later)
	}
	if got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-1"); err != nil || !got.ExpiresAt.Equal(later) {
		t.Fatalf("stored branch = %+v, %v", got, err)
	}
	if _, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", branch.BranchPoint.Add(-time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("expiry before the branch point: %v; want ErrInvalidArgument", err)
	}
	if _, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, "production", later); !errors.Is(err, state.ErrProjectEnvironmentNotBranch) {
		t.Fatalf("production: %v; want ErrProjectEnvironmentNotBranch", err)
	}
	if _, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, "missing", later); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing environment: %v; want ErrNotFound", err)
	}

	// A branch whose parent binds managed PostgreSQL is pending until its
	// copies exist; its expiry stays put until then.
	if err := s.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed"), ManagedPostgresBindingID: uuid.NewString(), ManagedCredentialRef: "ref-1", ManagedCredentialGeneration: 1,
		ManagedPostgresAccess: "read_write"}); err != nil {
		t.Fatal(err)
	}
	_, pending, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-2",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
	}, limits)
	if err != nil || pending.DataState != state.EnvironmentBranchDataPending {
		t.Fatalf("pending branch = %+v, %v", pending, err)
	}
	if _, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-2", later); !errors.Is(err, state.ErrProjectEnvironmentBranchDataPending) {
		t.Fatalf("pending data: %v; want ErrProjectEnvironmentBranchDataPending", err)
	}
	if got, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-2"); err != nil || !got.ExpiresAt.Equal(pending.ExpiresAt) {
		t.Fatalf("pending branch after a refused extend = %+v, %v", got, err)
	}
}

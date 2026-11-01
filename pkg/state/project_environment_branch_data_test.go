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

// seedProjectEnvironmentBranch publishes a live production deployment of one
// workload and branches production into pr-1 with that workload's deployment.
func seedProjectEnvironmentBranch(t *testing.T, s contractTestStore, email string) (state.Account, state.Project, state.ProjectEnvironmentBranch, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "data"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:data"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/data.ext4", "layers/data.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	_, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return acct, project, branch, deployment
}

// ADR-20261031193635 slice 2: a branch's database copies are recorded before
// they exist and outlive the branch until the reconciler removes them.
func TestMemProjectEnvironmentBranchDatabaseCopies(t *testing.T) {
	projectEnvironmentBranchDatabaseCopies(t, state.NewMemStore())
}

func projectEnvironmentBranchDatabaseCopies(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, branch, deployment := seedProjectEnvironmentBranch(t, s, "branch-data@example.com")
	if branch.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("a parent without managed data branches ready: %+v", branch)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, deployment.ID); err != nil || !ready {
		t.Fatalf("data ready = %v, %v", ready, err)
	}
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, uuid.NewString(), time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ready branch claimed: %v", err)
	}
	awaiting, err := s.ProjectEnvironmentBranchDeploymentsAwaitingActivation(ctx, branch.EnvironmentID)
	if err != nil || len(awaiting) != 1 || awaiting[0].ID != deployment.ID {
		t.Fatalf("awaiting activation = %+v, %v", awaiting, err)
	}

	source := uuid.NewString()
	copy := state.ProjectEnvironmentBranchDatabase{AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: branch.EnvironmentID,
		EnvironmentSlug: branch.Slug, SourceDatabaseID: source, CopyName: "br-pr-1-0001", PointInTime: branch.BranchPoint}
	recorded, err := s.RecordProjectEnvironmentBranchDatabase(ctx, copy)
	if err != nil || recorded.ID == "" || recorded.CopyDatabaseID != "" {
		t.Fatalf("record = %+v, %v", recorded, err)
	}
	again, err := s.RecordProjectEnvironmentBranchDatabase(ctx, copy)
	if err != nil || again.ID != recorded.ID {
		t.Fatalf("second record = %+v, %v", again, err)
	}
	other := copy
	other.SourceDatabaseID = uuid.NewString()
	if _, err := s.RecordProjectEnvironmentBranchDatabase(ctx, other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reused copy name: %v", err)
	}
	copyID := uuid.NewString()
	if err := s.SetProjectEnvironmentBranchDatabaseCopy(ctx, recorded.ID, copyID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectEnvironmentBranchDatabaseCopy(ctx, recorded.ID, copyID); err != nil {
		t.Fatalf("idempotent set: %v", err)
	}
	if err := s.SetProjectEnvironmentBranchDatabaseCopy(ctx, recorded.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replaced copy: %v", err)
	}
	if listed, err := s.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID); err != nil || len(listed) != 1 || listed[0].CopyDatabaseID != copyID {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if _, err := s.ClaimOrphanedProjectEnvironmentBranchDatabase(ctx, uuid.NewString(), time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a live branch's copy claimed as orphan: %v", err)
	}

	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, branch.Slug); err != nil {
		t.Fatal(err)
	}
	lease := uuid.NewString()
	orphan, err := s.ClaimOrphanedProjectEnvironmentBranchDatabase(ctx, lease, time.Now(), time.Minute)
	if err != nil || orphan.ID != recorded.ID || orphan.CopyDatabaseID != copyID || orphan.LeaseToken != lease {
		t.Fatalf("orphan = %+v, %v", orphan, err)
	}
	if _, err := s.ClaimOrphanedProjectEnvironmentBranchDatabase(ctx, uuid.NewString(), time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("leased orphan claimed twice: %v", err)
	}
	if err := s.RemoveProjectEnvironmentBranchDatabase(ctx, orphan.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("remove without the lease: %v", err)
	}
	if err := s.RemoveProjectEnvironmentBranchDatabase(ctx, orphan.ID, lease); err != nil {
		t.Fatal(err)
	}
	if listed, err := s.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID); err != nil || len(listed) != 0 {
		t.Fatalf("removed copy still listed: %+v, %v", listed, err)
	}
}

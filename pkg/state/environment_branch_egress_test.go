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

// ADR-20261031193635 slice 6a: vmmd reads a wake's containment from the
// deployment's branch, and refuses a deployment whose environment is gone.
func TestMemEnvironmentBranchEgress(t *testing.T) {
	environmentBranchEgress(t, state.NewMemStore())
}

func environmentBranchEgress(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, live, liveDeployment := seedProjectEnvironmentBranch(t, s, "branch-egress@example.com")
	parentID, err := s.EnvironmentBranchLineageParent(ctx, liveDeployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.EnvironmentBranchEgress(ctx, parentID); err != nil || got.Mode != "" || got.Contained() {
		t.Fatalf("production deployment egress = %+v, %v", got, err)
	}
	if got, err := s.EnvironmentBranchEgress(ctx, liveDeployment.ID); err != nil || got.Mode != state.EnvironmentBranchEgressLive ||
		got.Contained() || got.EnvironmentID != live.EnvironmentID || got.Scope != "pr-1" {
		t.Fatalf("live branch egress = %+v, %v", got, err)
	}
	if _, err := s.EnvironmentBranchEgress(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrEnvironmentBranchGone) {
		t.Fatalf("missing deployment: %v", err)
	}

	limits := api.MustLimitsFor(acct.Plan)
	_, contained, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-2",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressContained,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	containedDeployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-2", liveDeployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.EnvironmentBranchEgress(ctx, containedDeployment.ID)
	if err != nil || got.Mode != state.EnvironmentBranchEgressContained || !got.Contained() || got.EnvironmentID != contained.EnvironmentID {
		t.Fatalf("contained branch egress = %+v, %v", got, err)
	}
	if endpoints, err := s.EnvironmentBranchDatabaseEndpoints(ctx, containedDeployment.ID); err != nil || len(endpoints) != 0 {
		t.Fatalf("endpoints without bindings = %+v, %v", endpoints, err)
	}
	if endpoints, err := s.EnvironmentBranchDatabaseEndpoints(ctx, uuid.NewString()); err != nil || len(endpoints) != 0 {
		t.Fatalf("endpoints of a missing deployment = %+v, %v", endpoints, err)
	}

	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnvironmentBranchEgress(ctx, containedDeployment.ID); !errors.Is(err, state.ErrEnvironmentBranchGone) {
		t.Fatalf("a deleted branch's deployment must be refused, got %v", err)
	}
}

// A contained branch uses object storage only when the request admits the
// bridge object relay (slice 6c); without it the overlay buckets are served
// only by the public S3 gateway, which the contained profile does not reach.
func TestMemContainedEnvironmentBranchRefusesObjectStorage(t *testing.T) {
	containedEnvironmentBranchRefusesObjectStorage(t, state.NewMemStore())
}

func containedEnvironmentBranchRefusesObjectStorage(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, _, live := seedProjectEnvironmentBranch(t, s, "branch-objects@example.com")
	if err := s.PutManagedObjectStorageSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: live.AppID, Scope: "production",
		Key: "FILES_BUCKET", Ciphertext: []byte("sealed"), ManagedObjectStorageCredentialID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	create := func(slug, mode string, relay bool) error {
		_, _, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: slug,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: mode,
			MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
			ContainedObjects: relay,
		}, limits)
		return err
	}
	if err := create("pr-2", state.EnvironmentBranchEgressContained, false); !errors.Is(err, state.ErrProjectEnvironmentBranchContainedObjects) {
		t.Fatalf("contained branch with object storage: %v", err)
	}
	if err := create("pr-3", state.EnvironmentBranchEgressLive, false); err != nil {
		t.Fatalf("live branch with object storage: %v", err)
	}
	if err := create("pr-4", state.EnvironmentBranchEgressContained, true); err != nil {
		t.Fatalf("contained branch through the object relay: %v", err)
	}
}

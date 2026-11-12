//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentBranchDatabaseCopies(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchDatabaseCopies(t, s)
}

// A pending branch is leased to one reconciler at a time, retried with
// backoff, and keeps its workloads dark until its data is ready.
func TestPgProjectEnvironmentBranchDataClaim(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	_, _, branch, deployment := seedProjectEnvironmentBranch(t, s, "branch-claim@example.com")
	if _, err := pool.Exec(ctx, `update project_environment_branches set data_state = 'pending' where environment_id = $1`, branch.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, deployment.ID); err != nil || ready {
		t.Fatalf("pending data ready = %v, %v", ready, err)
	}
	now := time.Now()
	lease := uuid.NewString()
	claimed, err := s.ClaimProjectEnvironmentBranchData(ctx, lease, now, time.Minute)
	if err != nil || claimed.EnvironmentID != branch.EnvironmentID || claimed.DataLeaseToken != lease || claimed.ParentSlug != "production" {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("leased branch claimed twice: %v", err)
	}
	if err := s.CompleteProjectEnvironmentBranchData(ctx, branch.EnvironmentID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("complete without the lease: %v", err)
	}
	if err := s.RetryProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease, now.Add(time.Hour), "restore not ready", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimProjectEnvironmentBranchData(ctx, uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("backed-off branch claimed early: %v", err)
	}
	lease = uuid.NewString()
	claimed, err = s.ClaimProjectEnvironmentBranchData(ctx, lease, now.Add(2*time.Hour), time.Minute)
	if err != nil || claimed.DataAttempts != 1 || claimed.DataError != "restore not ready" {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	if err := s.CompleteProjectEnvironmentBranchData(ctx, branch.EnvironmentID, lease); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.EnvironmentBranchDataReady(ctx, deployment.ID); err != nil || !ready {
		t.Fatalf("completed data ready = %v, %v", ready, err)
	}
	got, err := s.ProjectEnvironmentBranchBySlug(ctx, branch.AccountID, branch.ProjectID, branch.Slug)
	if err != nil || got.DataState != state.EnvironmentBranchDataReady || got.DataError != "" || got.DataLeaseToken != "" {
		t.Fatalf("completed branch = %+v, %v", got, err)
	}
}

package sched_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeClaimBackend func(context.Context, sched.AppTaskRestoreRequest) (sched.AppTaskSession, error)

func (f nodeClaimBackend) Restore(ctx context.Context, request sched.AppTaskRestoreRequest) (sched.AppTaskSession, error) {
	return f(ctx, request)
}

func newNodeOwnedAppTask(t *testing.T, ownerNodeID string) (*state.MemStore, state.AppTask) {
	t.Helper()
	return newNodeOwnedAppTaskOfKind(t, ownerNodeID, state.AppTaskKindManual)
}

func newNodeOwnedAppTaskOfKind(t *testing.T, ownerNodeID string, kind state.AppTaskKind) (*state.MemStore, state.AppTask) {
	t.Helper()
	store := state.NewMemStore()
	return store, newNodeOwnedAppTaskIn(t, store, ownerNodeID, kind)
}

func newNodeOwnedAppTaskIn(t *testing.T, store state.Store, ownerNodeID string, kind state.AppTaskKind) state.AppTask {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "app-task-node-claim@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-node-claim", Type: state.AppTypeApp, NodeID: ownerNodeID,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app-task-node-claim", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task.ext4", "apps/app-task/node-claim.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	if _, ok := store.(*state.PgStore); ok {
		// PostgreSQL creates customer intent as pending whatever status is
		// supplied; model imaged's transition so the artifact is usable.
		if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeploySnapshotting, ""); err != nil {
			t.Fatalf("UpdateDeploymentStatus: %v", err)
		}
	}
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: kind, Command: []string{"/bin/true"}, CreatedAt: time.Now().UTC().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	return task
}

func nodeClaimConfig(nodeID string) sched.AppTaskCoordinatorConfig {
	return sched.AppTaskCoordinatorConfig{
		Owner: nodeID, NodeID: nodeID, MaxConcurrent: 1,
		LeaseDuration: time.Minute, LeaseRenewInterval: 10 * time.Millisecond,
		RestoreTimeout: time.Second, DestroyTimeout: time.Second, FinalizeTimeout: time.Second,
	}
}

// A schedd must not claim a task for an app another node owns: its placement
// refuses that app, and the task used to fail with restore_failed before it
// ran (about half of `gregale app exec` calls on a two-node fleet).
func TestAppTaskCoordinatorClaimsOnlyOwnedApps(t *testing.T) {
	const owner, peer = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	store, task := newNodeOwnedAppTask(t, owner)
	restores := 0
	backend := nodeClaimBackend(func(context.Context, sched.AppTaskRestoreRequest) (sched.AppTaskSession, error) {
		restores++
		return nil, fmt.Errorf("placement: %w", sched.ErrAppTaskNotOwner)
	})

	processed, err := sched.NewAppTaskCoordinator(store, backend, nodeClaimConfig(peer), nil).ProcessNext(context.Background())
	if err != nil || processed || restores != 0 {
		t.Fatalf("peer ProcessNext = %v, %v (restores %d), want no claim", processed, err, restores)
	}

	processed, err = sched.NewAppTaskCoordinator(store, backend, nodeClaimConfig(owner), nil).ProcessNext(context.Background())
	if err != nil || !processed || restores != 1 {
		t.Fatalf("owner ProcessNext = %v, %v (restores %d), want one claim", processed, err, restores)
	}
	// An ownership refusal after the claim (the owner changed in between) dispatched
	// nothing, so the task stays restoring for the sweep to requeue, not failed.
	got, err := store.AppTaskByID(context.Background(), task.AccountID, task.AppID, task.ID)
	if err != nil || got.Status != state.AppTaskRestoring || got.FailureCode != nil {
		t.Fatalf("task after ownership refusal = %+v, err=%v; want restoring without failure", got, err)
	}
}

package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// newCapacityWaitAppTask is newNodeOwnedAppTask with the task's creation time
// under the test's control.
func newCapacityWaitAppTask(t *testing.T, nodeID string, createdAt time.Time) (*state.MemStore, state.AppTask) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "app-task-capacity-wait@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-capacity-wait", Type: state.AppTypeApp, NodeID: nodeID,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app-task-capacity-wait", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task.ext4", "apps/app-task/capacity-wait.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"/bin/true"}, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	return store, task
}

// A release task whose environment no node had room for failed for good, and
// its deployment with it, though no command had run (production 2026-11-05:
// eight placement refusals on compute-1 within five minutes). Inside
// api.AppTaskCapacityMaxWait the refusal now leaves the task restoring for the
// lease sweep to requeue; after it the task fails as capacity_unavailable, a
// platform code imaged reports as release_infrastructure_failed.
func TestAppTaskCoordinatorWaitsForNodeCapacity(t *testing.T) {
	const node = "11111111-1111-4111-8111-111111111111"
	refusal := sched.NodeBudgetRefusal("placement: no active compute_node fits",
		api.WorkerAdmissionBlocker{Code: "node_vcpu", Resource: "vcpu", Requested: ptrInt64(4)})
	backend := nodeClaimBackend(func(context.Context, sched.AppTaskRestoreRequest) (sched.AppTaskSession, error) {
		return nil, refusal
	})
	for _, tc := range []struct {
		name     string
		age      time.Duration
		wantWait bool
	}{
		{name: "inside the window", age: time.Second, wantWait: true},
		{name: "after the window", age: api.AppTaskCapacityMaxWait + time.Minute, wantWait: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, task := newCapacityWaitAppTask(t, node, time.Now().UTC().Add(-tc.age))
			processed, err := sched.NewAppTaskCoordinator(store, backend, nodeClaimConfig(node), nil).ProcessNext(context.Background())
			if err != nil || !processed {
				t.Fatalf("ProcessNext = %v, %v; want one claim without error", processed, err)
			}
			got, err := store.AppTaskByID(context.Background(), task.AccountID, task.AppID, task.ID)
			if err != nil {
				t.Fatalf("AppTaskByID: %v", err)
			}
			if tc.wantWait {
				if got.Status != state.AppTaskRestoring || got.FailureCode != nil {
					t.Fatalf("task = %s (failure %v); want restoring without failure", got.Status, got.FailureCode)
				}
				return
			}
			if got.Status != state.AppTaskFailed || got.FailureCode == nil || *got.FailureCode != "capacity_unavailable" {
				t.Fatalf("task = %s (failure %v); want failed with capacity_unavailable", got.Status, got.FailureCode)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

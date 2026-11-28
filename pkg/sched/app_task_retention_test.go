package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// PruneOnce applies each plan's Limits.AppTaskRetentionDays: a Free task
// finished past 7 days is deleted, a Pro task of the same age is kept.
func TestAppTaskCoordinatorPrunesPerPlanRetention(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	now := time.Now().UTC()
	finishedAt := now.AddDate(0, 0, -(api.MustLimitsFor(api.PlanFree).AppTaskRetentionDays + 1))
	var ids [2]string
	var owners [2][2]string
	for i, plan := range []api.Plan{api.PlanFree, api.PlanPro} {
		account, err := store.CreateAccount(ctx, string(plan)+"-prune@example.com", plan)
		if err != nil {
			t.Fatal(err)
		}
		limits := api.MustLimitsFor(plan)
		app, err := store.CreateAppIfUnderQuota(ctx, state.App{
			AccountID: account.ID, Slug: string(plan) + "-prune", Type: state.AppTypeApp,
			Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
		}, limits)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:prune", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/prune.ext4", "apps/prune/rootfs.ext4", 4096); err != nil {
			t.Fatal(err)
		}
		task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
			AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
			Kind: state.AppTaskKindManual, Command: []string{"bin/work"}, TimeoutSeconds: 30, CreatedAt: finishedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RequestAppTaskCancellation(ctx, account.ID, app.ID, task.ID, finishedAt); err != nil {
			t.Fatal(err)
		}
		ids[i], owners[i] = task.ID, [2]string{account.ID, app.ID}
	}

	coordinator := sched.NewAppTaskCoordinator(store, nil, sched.AppTaskCoordinatorConfig{}, nil)
	deleted, err := coordinator.PruneOnce(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("PruneOnce deleted %d, err %v; want 1", deleted, err)
	}
	if _, err := store.AppTaskByID(ctx, owners[0][0], owners[0][1], ids[0]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("free task past retention: err = %v, want ErrNotFound", err)
	}
	if _, err := store.AppTaskByID(ctx, owners[1][0], owners[1][1], ids[1]); err != nil {
		t.Fatalf("pro task within retention was pruned: %v", err)
	}
}

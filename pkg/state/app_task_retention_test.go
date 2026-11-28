package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPruneAppTasksMem(t *testing.T) {
	appTaskRetentionSuite(t, state.NewMemStore())
}

func TestPruneAppTasksPG(t *testing.T) {
	store, _ := pgStore(t)
	appTaskRetentionSuite(t, store)
}

type retentionFixture struct {
	store      state.Store
	accountID  string
	appID      string
	deployment string
}

func newRetentionFixture(t *testing.T, store state.Store, slug string, plan api.Plan) retentionFixture {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, slug+"@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(plan)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: slug, Type: state.AppTypeApp,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("3", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/"+slug+".ext4", "apps/"+slug+"/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return retentionFixture{store: store, accountID: account.ID, appID: app.ID, deployment: deployment.ID}
}

// task creates a manual task at createdAt and, when finish is set, cancels
// it while queued so it finishes at createdAt.
func (f retentionFixture) task(t *testing.T, createdAt time.Time, finish bool) string {
	t.Helper()
	ctx := context.Background()
	task, err := f.store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: f.accountID, AppID: f.appID, DeploymentID: f.deployment,
		Kind: state.AppTaskKindManual, Command: []string{"bin/work"}, TimeoutSeconds: 30, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finish {
		if _, err := f.store.RequestAppTaskCancellation(ctx, f.accountID, f.appID, task.ID, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	return task.ID
}

func (f retentionFixture) exists(t *testing.T, id string) bool {
	t.Helper()
	_, err := f.store.AppTaskByID(context.Background(), f.accountID, f.appID, id)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

func appTaskRetentionSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	pruner, ok := store.(state.AppTaskRetentionStore)
	if !ok {
		t.Fatal("store does not implement AppTaskRetentionStore")
	}
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -7)
	free := newRetentionFixture(t, store, "prune-free", api.PlanFree)
	pro := newRetentionFixture(t, store, "prune-pro", api.PlanPro)

	oldest := free.task(t, now.AddDate(0, 0, -30), true)
	older := free.task(t, now.AddDate(0, 0, -20), true)
	recent := free.task(t, now.AddDate(0, 0, -1), true)
	queued := free.task(t, now.AddDate(0, 0, -30), false)
	otherPlan := pro.task(t, now.AddDate(0, 0, -30), true)

	// The limit deletes oldest first.
	deleted, err := pruner.PruneAppTasks(ctx, state.PruneAppTasksParams{Plan: api.PlanFree, FinishedBefore: cutoff, Limit: 1})
	if err != nil || deleted != 1 {
		t.Fatalf("first pass deleted %d, err %v; want 1", deleted, err)
	}
	if free.exists(t, oldest) || !free.exists(t, older) {
		t.Fatal("a limited pass must delete the oldest finished task first")
	}
	deleted, err = pruner.PruneAppTasks(ctx, state.PruneAppTasksParams{Plan: api.PlanFree, FinishedBefore: cutoff, Limit: 100})
	if err != nil || deleted != 1 {
		t.Fatalf("second pass deleted %d, err %v; want 1", deleted, err)
	}
	if free.exists(t, older) {
		t.Fatal("a finished task past retention was kept")
	}
	if !free.exists(t, recent) || !free.exists(t, queued) || !pro.exists(t, otherPlan) {
		t.Fatal("pruned a recent, unfinished or other-plan task")
	}
	if _, err := pruner.PruneAppTasks(ctx, state.PruneAppTasksParams{Plan: api.PlanFree, FinishedBefore: cutoff}); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("zero limit: err = %v, want ErrAppTaskInvalid", err)
	}
}

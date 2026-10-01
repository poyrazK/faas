package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func TestExclusiveAppTaskGenerationFencesStaleCompletion(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemStoreWithExclusiveClock(func() time.Time { return now })
	account, err := store.CreateAccount(ctx, "app-task-fence@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, App{
		AccountID: account.ID, Slug: "app-task-fence", Type: AppTypeApp,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:app-task-fence", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task-fence.ext4", "apps/app-task-fence/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	owners := ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "app-task-fence", Scope: "account", MemberAppIDs: []string{app.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	op, _, err := owners.AdmitExclusiveOperation(ctx, ExclusiveAdmission{
		AccountID: account.ID, AppID: app.ID, PolicyName: "app-task-fence",
		Key: json.RawMessage(`"maintenance:acme"`), Request: json.RawMessage(`{"kind":"app_task"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstOwner, err := owners.ClaimExclusiveOperation(ctx, account.ID, op.ID, "app-task-owner/old")
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateAppTask(ctx, CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		ExclusiveOperationID: op.ID, ExclusiveGeneration: firstOwner.Generation,
		Kind: AppTaskKindManual, Command: []string{"bin/migrate"}, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "old-worker", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if running.ExclusiveGeneration != firstOwner.Generation {
		t.Fatalf("running task generation = %d, want %d", running.ExclusiveGeneration, firstOwner.Generation)
	}

	now = now.Add(6 * time.Second)
	secondOwner, err := owners.ClaimExclusiveOperation(ctx, account.ID, op.ID, "app-task-owner/new")
	if err != nil {
		t.Fatal(err)
	}
	if secondOwner.Generation <= firstOwner.Generation {
		t.Fatalf("replacement generation = %d, old generation = %d", secondOwner.Generation, firstOwner.Generation)
	}
	exitCode := 0
	_, err = store.CompleteAppTask(ctx, CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: AppTaskSucceeded,
		ExitCode: &exitCode, FinishedAt: now.Add(time.Second),
	})
	if !errors.Is(err, ErrAppTaskLeaseLost) {
		t.Fatalf("stale task completion = %v, want ErrAppTaskLeaseLost", err)
	}
	unchanged, err := store.AppTaskByID(ctx, account.ID, app.ID, task.ID)
	if err != nil || unchanged.Status != AppTaskRunning || unchanged.FinishedAt != nil {
		t.Fatalf("stale completion changed task: status=%s finished=%v err=%v", unchanged.Status, unchanged.FinishedAt, err)
	}
}

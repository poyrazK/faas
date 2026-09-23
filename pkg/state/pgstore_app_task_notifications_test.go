package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreReleaseTaskCompletionAndNotificationAreAtomic(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "release-outbox-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "release-outbox-" + uuid.NewString()[:8], RAMMB: 256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:release-outbox",
		Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/release.ext4", "apps/release/outbox.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	createdAt := time.Now().UTC()
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "schedd", createdAt.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, createdAt.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}

	if _, err := pool.Exec(ctx, `alter table notification_outbox add constraint reject_release_task_notification_test check (channel <> 'app_task_changed')`); err != nil {
		t.Fatalf("install outbox failure: %v", err)
	}
	exitZero := 0
	completion := state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exitZero, FinishedAt: createdAt.Add(3 * time.Second),
	}
	if _, err := store.CompleteAppTask(ctx, completion); err == nil {
		t.Fatal("release completion committed despite failed outbox enqueue")
	}
	stillRunning, err := store.AppTaskByID(ctx, account.ID, app.ID, task.ID)
	if err != nil || stillRunning.Status != state.AppTaskRunning {
		t.Fatalf("task after rolled-back completion = %+v, err=%v", stillRunning, err)
	}
	if _, err := pool.Exec(ctx, `alter table notification_outbox drop constraint reject_release_task_notification_test`); err != nil {
		t.Fatalf("remove outbox failure: %v", err)
	}
	completed, err := store.CompleteAppTask(ctx, completion)
	if err != nil {
		t.Fatalf("CompleteAppTask: %v", err)
	}

	var raw string
	if err := pool.QueryRow(ctx, `select payload from notification_outbox where channel = $1 order by id desc limit 1`, db.NotifyAppTaskChanged).Scan(&raw); err != nil {
		t.Fatalf("release task outbox row: %v", err)
	}
	var payload db.AppTaskChangedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode release task payload: %v", err)
	}
	if payload.TaskID != completed.ID || payload.DeploymentID != deployment.ID ||
		payload.Kind != string(state.AppTaskKindRelease) || payload.Status != string(state.AppTaskSucceeded) {
		t.Fatalf("release task payload = %+v", payload)
	}
}

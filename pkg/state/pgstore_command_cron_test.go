package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgScheduledCommandCronCreatesCursorGuardedTask(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "command-cron-"+uuid.NewString(), "command-cron-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/cron.ext4", "apps/cron/rootfs.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/", true, state.CronOptions{
		Command: []string{"bin/maintenance", "--compact"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Now().UTC().Truncate(time.Minute)
	task, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTask = %+v, created=%t, err=%v", task, created, err)
	}
	if task.CronID != cron.ID || task.Kind != state.AppTaskKindCron || task.DeploymentID != deploymentID ||
		task.ScheduledFor == nil || !task.ScheduledFor.Equal(firedAt) || len(task.Command) != 2 {
		t.Fatalf("scheduled task did not round-trip cron metadata: %+v", task)
	}

	duplicate, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || created || duplicate.ID != "" {
		t.Fatalf("stale scheduler snapshot = %+v, created=%t, err=%v; want no-op", duplicate, created, err)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(firedAt) {
		t.Fatalf("cron cursor = %v, %v; want %v", storedCron.LastFiredAt, err, firedAt)
	}
	if active, err := store.CountActiveCronAppTasks(ctx, cron.ID); err != nil || active != 1 {
		t.Fatalf("CountActiveCronAppTasks = %d, %v; want 1", active, err)
	}
	runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 || runs[0].ID != task.ID {
		t.Fatalf("ListCronAppTaskRuns = %+v, %v; want the created task", runs, err)
	}
}

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Cost-control review 2026-11-19 (N16): meterd finds the app tasks whose VMs
// ran in a sampled minute, finished ones included, and no others.
func exerciseAppTaskBillingWindow(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	billing, ok := store.(state.AppTaskBillingStore)
	if !ok {
		t.Fatal("store does not list app tasks for billing")
	}
	account, err := store.CreateAccount(ctx, "app-task-window@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-window", Type: state.AppTypeApp, Runtime: "node22",
		RAMMB: 512, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app-task-window", Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := store.UpdateDeploymentStatus(ctx, deployment.ID, status, ""); err != nil {
			t.Fatalf("UpdateDeploymentStatus(%s): %v", status, err)
		}
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task.ext4", "apps/app-task/deployment.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Kind: state.AppTaskKindManual,
		Command: []string{"bin/report"}, TimeoutSeconds: 600, MaxOutputBytes: 2048, CreatedAt: start.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	minuteOf := func(offset time.Duration) []state.AppTaskBillingWindow {
		t.Helper()
		windows, err := billing.ListAppTasksInBillingWindow(ctx, start.Add(offset), start.Add(offset+time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return windows
	}
	if got := minuteOf(0); len(got) != 0 {
		t.Fatalf("queued task listed: %+v", got)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "schedd-test", start, time.Hour)
	if err != nil || claimed.LeaseToken == nil {
		t.Fatalf("ClaimNextAppTask = %+v, %v", claimed, err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, start.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if _, err := store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit,
		FinishedAt: start.Add(90 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset time.Duration
		listed bool
	}{{-time.Minute, false}, {0, true}, {time.Minute, true}, {2 * time.Minute, false}} {
		got := minuteOf(tc.offset)
		if listed := len(got) == 1 && got[0].TaskID == task.ID && got[0].AppID == app.ID; listed != tc.listed {
			t.Fatalf("minute %v: listed = %+v, want listed %v", tc.offset, got, tc.listed)
		}
	}
	got := minuteOf(time.Minute)[0]
	if seconds := got.BillableSeconds(start.Add(time.Minute), start.Add(2*time.Minute), time.Now()); seconds != 30 {
		t.Fatalf("second minute billable seconds = %d, want 30", seconds)
	}
	if _, err := billing.ListAppTasksInBillingWindow(ctx, start, start.Add(2*time.Minute)); err == nil {
		t.Fatal("a window longer than a minute was accepted")
	}
}

func TestMemStoreAppTaskBillingWindow(t *testing.T) {
	exerciseAppTaskBillingWindow(t, state.NewMemStore())
}

func TestPgStoreAppTaskBillingWindow(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAppTaskBillingWindow(t, state.NewPgStore(pool))
}

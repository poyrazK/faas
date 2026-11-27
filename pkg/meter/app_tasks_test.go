package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Cost-control review 2026-11-19 (N16): an app task VM holds the app's RAM
// but has no instance row. It used to bill nothing; it now bills plan RAM +
// 8 MB per running second under the app, keyed by the task.
func TestRollAppsBillsRunningAppTasks(t *testing.T) {
	exerciseRollAppsBillsRunningAppTasks(t, state.NewMemStore())
}

func TestPgRollAppsBillsRunningAppTasks(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseRollAppsBillsRunningAppTasks(t, state.NewPgStore(pool))
}

func exerciseRollAppsBillsRunningAppTasks(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "app-task-billing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-billing", Type: state.AppTypeApp, Runtime: "node22",
		RAMMB: 512, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app-task-billing", Status: state.DeploySnapshotting,
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
	minute := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Kind: state.AppTaskKindManual,
		Command: []string{"bin/report"}, TimeoutSeconds: 600, MaxOutputBytes: 2048, CreatedAt: minute.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "schedd-test", minute.Add(-time.Minute), 10*time.Minute)
	if err != nil || claimed.ID != task.ID || claimed.LeaseToken == nil {
		t.Fatalf("ClaimNextAppTask = %+v, %v", claimed, err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, minute.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	observedAt := minute.Add(90 * time.Second)
	s := NewSampler(store, nil, func() time.Time { return observedAt })
	rows, err := s.rollApps(ctx, observedAt, minute, false, false)
	if err != nil {
		t.Fatalf("rollApps: %v", err)
	}
	var billed *RolledRow
	for i := range rows {
		if rows[i].InstanceID == task.ID {
			billed = &rows[i]
		}
	}
	want := int64(api.BillableRAMMBWithSidecars(512, nil)) * 60
	if billed == nil || billed.AppID != app.ID || billed.AccountID != account.ID || billed.MBSeconds != want {
		t.Fatalf("task row = %+v, want app %s account %s mb_seconds %d", billed, app.ID, account.ID, want)
	}
	usage, err := store.UsageByHour(ctx, account.ID, minute.Truncate(time.Hour), minute.Truncate(time.Hour).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, u := range usage {
		total += u.MBSeconds
	}
	if total != want {
		t.Fatalf("billed hour mb_seconds = %d, want %d", total, want)
	}
}

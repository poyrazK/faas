package state

import (
	"testing"
	"time"
)

// adr: 099 — scheduled commands select the current live deployment once per fire.
func TestMemStoreScheduledCommandCronUsesCurrentLiveDeploymentOnce(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	firstLive, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:first", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment(first): %v", err)
	}
	if err := m.SetDeploymentRootfs(ctx, firstLive.ID, "/rootfs/first", "apps/first.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(first): %v", err)
	}
	cron, err := m.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command: []string{"bin/maintenance", "--compact"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}

	firedAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first, created, err := m.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("first fire = task %+v, created=%t, err=%v", first, created, err)
	}
	if first.Kind != AppTaskKindCron || first.CronID != cron.ID || first.DeploymentID != firstLive.ID ||
		first.ScheduledFor == nil || !first.ScheduledFor.Equal(firedAt) || first.TimeoutSeconds != AppTaskDefaultTimeoutSeconds {
		t.Fatalf("first task did not preserve cron/deployment metadata: %+v", first)
	}

	// A deployment made live after the schedule was created must be the one
	// selected by the next fire, rather than a deployment pinned at cron setup.
	secondLive, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:second", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: firedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateDeployment(second): %v", err)
	}
	if err := m.SetDeploymentRootfs(ctx, secondLive.ID, "/rootfs/second", "apps/second.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(second): %v", err)
	}

	secondAt := firedAt.Add(2 * time.Minute)
	second, created, err := m.CreateScheduledCronAppTask(ctx, cron.ID, &firedAt, secondAt)
	if err != nil || !created {
		t.Fatalf("second fire = task %+v, created=%t, err=%v", second, created, err)
	}
	if second.DeploymentID != secondLive.ID {
		t.Fatalf("second task deployment = %q, want current live %q", second.DeploymentID, secondLive.ID)
	}

	duplicate, created, err := m.CreateScheduledCronAppTask(ctx, cron.ID, &firedAt, secondAt)
	if err != nil || created || duplicate.ID != "" {
		t.Fatalf("stale duplicate fire = task %+v, created=%t, err=%v; want no-op", duplicate, created, err)
	}
	if active, err := m.CountActiveCronAppTasks(ctx, cron.ID); err != nil || active != 2 {
		t.Fatalf("active command runs = %d, %v; want 2", active, err)
	}
	runs, err := m.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 2 || runs[0].ID != second.ID || runs[1].ID != first.ID {
		t.Fatalf("command cron runs = %+v, %v; want newest-first history", runs, err)
	}
}

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// adr: 099 — scheduled commands run as tasks on the current live deployment.
func TestDispatchCommandCronQueuesOneTaskPerFireOnCurrentDeployment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, firstDeployment := seedApp(t, store, api.PlanPro, 256, 1)
	if err := store.SetDeploymentRootfs(ctx, firstDeployment.ID, "/rootfs/first", "apps/first.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(first): %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	createdAt := time.Now().UTC().Add(-time.Hour)
	cron, err = store.UpdateCron(ctx, cron.ID, nil, nil, nil, &createdAt)
	if err != nil {
		t.Fatalf("backdate cron: %v", err)
	}
	engine := newEngine(t, store, &fakeWakeVMM{}, nil, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	firedAt := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	loop.dispatchOneCron(ctx, cron, firedAt)
	runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs after first fire = %d, %v; want one", len(runs), err)
	}
	if runs[0].DeploymentID != firstDeployment.ID || runs[0].Kind != state.AppTaskKindCron {
		t.Fatalf("first scheduled task = %+v; want deployment %s, kind cron", runs[0], firstDeployment.ID)
	}

	// A scheduler with a stale list snapshot must not enqueue the same
	// occurrence again; the state store's cursor compare-and-set owns this.
	loop.dispatchOneCron(ctx, cron, firedAt)
	runs, err = store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs after duplicate snapshot = %d, %v; want one", len(runs), err)
	}

	secondDeployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:next", Status: state.DeployLive,
		Kind: state.DeploymentKindImage, CreatedAt: firedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateDeployment(second): %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, secondDeployment.ID, "/rootfs/second", "apps/second.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(second): %v", err)
	}
	cron, err = store.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatalf("reload cron: %v", err)
	}
	loop.dispatchOneCron(ctx, cron, firedAt.Add(2*time.Minute))
	runs, err = store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs after second fire = %d, %v; want two", len(runs), err)
	}
	if runs[0].DeploymentID != secondDeployment.ID {
		t.Fatalf("second run deployment = %q, want current deployment %q", runs[0].DeploymentID, secondDeployment.ID)
	}
}

func TestDispatchScheduledHTTPCronQueuesDurableOccurrence(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 1)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/internal/sync", true, state.CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "skip", StartDeadlineSeconds: 120, MissedRuns: "coalesce_latest",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Now().UTC().Truncate(time.Minute)
	createdAt := firedAt.Add(-time.Minute)
	cron, err = store.UpdateCron(ctx, cron.ID, nil, nil, nil, &createdAt)
	if err != nil {
		t.Fatalf("backdate cron: %v", err)
	}
	engine := newEngine(t, store, &fakeWakeVMM{}, nil, "1.10.0")
	loop := NewLoop(nil, engine, testLog())
	loop.dispatchOneCron(ctx, cron, firedAt.Add(5*time.Second))

	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 1 || history[0].Status != "queued" || history[0].InvocationID == "" {
		t.Fatalf("scheduled HTTP history = %+v, %v; want linked queued occurrence", history, err)
	}
	invocation, err := store.InvocationByID(ctx, history[0].InvocationID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if invocation.State != state.InvocationPending || invocation.Source != state.InvocationCron ||
		invocation.Method != "POST" || invocation.Path != "/internal/sync" || invocation.OccurrenceID != history[0].ID ||
		invocation.StartDeadlineAt == nil || !invocation.StartDeadlineAt.Equal(firedAt.Add(120*time.Second)) {
		t.Fatalf("queued scheduled invocation = %+v; want pending cron with occurrence and deadline", invocation)
	}
	cron, err = store.CronByID(ctx, cron.ID)
	if err != nil || !cron.LastFiredAt.Equal(firedAt) {
		t.Fatalf("cron cursor = %v, %v; want %v", cron.LastFiredAt, err, firedAt)
	}
}

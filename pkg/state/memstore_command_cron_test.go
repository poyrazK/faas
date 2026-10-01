package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
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

func TestMemStoreHTTPCronRejectsDeploymentWorkPolicies(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	_, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "replace", MissedRuns: "skip"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateCronWithOptions(HTTP with deployment schedule policy) = %v; want ErrInvalidArgument", err)
	}
}

func TestMemStoreScheduledCronOccurrenceRecordsSkipAndDeadline(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:policy-cron", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/policy-cron", "apps/policy-cron.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command: []string{"bin/synchronize"},
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "skip", StartDeadlineSeconds: 60, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first, firstOccurrence, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firstAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || !created || first.OccurrenceID != firstOccurrence.ID || firstOccurrence.Status != "queued" {
		t.Fatalf("first scheduled occurrence = task %+v, occurrence %+v, created=%t, err=%v", first, firstOccurrence, created, err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "cron-worker", firstAt.Add(30*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, first.ID, *claimed.LeaseToken, firstAt.Add(31*time.Second)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	secondAt := firstAt.Add(time.Minute)
	_, skipped, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &firstAt, secondAt,
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || created || skipped.Status != "skipped_overlap" || skipped.BlockingOccurrenceID != firstOccurrence.ID {
		t.Fatalf("overlap occurrence = %+v, created=%t, err=%v; want an explained skip", skipped, created, err)
	}
	thirdAt := secondAt.Add(time.Minute)
	_, missed, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &secondAt, thirdAt.Add(2*time.Minute),
		CronScheduledOccurrenceOptions{ScheduledFor: thirdAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || created || missed.Status != "missed_deadline" || missed.Reason == "" {
		t.Fatalf("late occurrence = %+v, created=%t, err=%v; want missed_deadline", missed, created, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 3 || history[0].Status != "missed_deadline" || history[1].Status != "skipped_overlap" || history[2].Status != "running" {
		t.Fatalf("cron occurrence history = %+v, %v; want missed, skipped, and running", history, err)
	}
}

func TestMemStoreCommandCronStartDeadlineAppliesToFirstStart(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:deadline-cron", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/deadline-cron", "apps/deadline-cron.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command:        []string{"bin/synchronize"},
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 5, MissedRuns: "skip"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	task, occurrence, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firedAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTaskOccurrence = %+v, %+v, %t, %v", task, occurrence, created, err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "cron-worker", firedAt.Add(5*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("claim exactly at inclusive deadline: %v", err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, firedAt.Add(6*time.Second)); !errors.Is(err, ErrAppTaskLeaseLost) {
		t.Fatalf("late MarkAppTaskRunning = %v; want lease lost after deadline", err)
	}
	rows, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(rows) != 1 || rows[0].Status != "missed_deadline" {
		t.Fatalf("occurrence after late first start = %+v, %v; want missed_deadline", rows, err)
	}
}

func TestMemStoreCommandCronReaperHonorsUncertainOutcomePolicy(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:uncertain-cron", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/uncertain-cron", "apps/uncertain-cron.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	for i, uncertainOutcome := range []string{"hold", "retry"} {
		cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
			Command: []string{"bin/synchronize"}, RetryMax: 1,
			FailureRules: &workpolicy.FailureRules{
				Version: workpolicy.Version, UnmatchedFailure: "retry", UncertainOutcome: uncertainOutcome,
			},
		})
		if err != nil {
			t.Fatalf("CreateCronWithOptions(%s): %v", uncertainOutcome, err)
		}
		firedAt := time.Date(2026, 9, 25, 10, i, 0, 0, time.UTC)
		task, _, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firedAt,
			CronScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: cron.ScheduleRevision})
		if err != nil || !created {
			t.Fatalf("CreateScheduledCronAppTaskOccurrence(%s) = %+v, %t, %v", uncertainOutcome, task, created, err)
		}
		claimed, err := store.ClaimNextAppTask(ctx, "cron-worker", firedAt, time.Second)
		if err != nil {
			t.Fatalf("ClaimNextAppTask(%s): %v", uncertainOutcome, err)
		}
		if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, firedAt.Add(100*time.Millisecond)); err != nil {
			t.Fatalf("MarkAppTaskRunning(%s): %v", uncertainOutcome, err)
		}
		sweep, err := store.SweepExpiredAppTasks(ctx, firedAt.Add(2*time.Second))
		if err != nil {
			t.Fatalf("SweepExpiredAppTasks(%s): %v", uncertainOutcome, err)
		}
		recovered, err := store.AppTaskByID(ctx, app.AccountID, app.ID, task.ID)
		if err != nil || recovered.WorkDecision == nil || recovered.WorkDecision.Classification != "uncertain" || recovered.WorkDecision.Action != uncertainOutcome {
			t.Fatalf("expired task(%s) = %+v, %v; want recorded uncertain decision", uncertainOutcome, recovered, err)
		}
		if uncertainOutcome == "hold" && (recovered.Status != AppTaskFailed || sweep.FailedRuns != 1) {
			t.Fatalf("hold result = task %+v, sweep %+v; want terminal failed run", recovered, sweep)
		}
		if uncertainOutcome == "retry" && (recovered.Status != AppTaskQueued || recovered.RetryAt == nil || sweep.FailedRuns != 0) {
			t.Fatalf("retry result = task %+v, sweep %+v; want bounded retry queued", recovered, sweep)
		}
	}
}

func TestMemStoreScheduledCronReplaceWaitsForConfirmedCancellation(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:replace-cron", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/replace-cron", "apps/replace-cron.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command:        []string{"bin/synchronize"},
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "replace", MissedRuns: "skip"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first, _, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firstAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || !created {
		t.Fatalf("first occurrence = %+v, created=%t, err=%v", first, created, err)
	}
	secondAt := firstAt.Add(time.Minute)
	_, pending, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &firstAt, secondAt,
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || created || pending.ID != "" {
		t.Fatalf("replace while prior task active = %+v, created=%t, err=%v; want cursor-preserving wait", pending, created, err)
	}
	stored, err := store.CronByID(ctx, cron.ID)
	if err != nil || !stored.LastFiredAt.Equal(firstAt) {
		t.Fatalf("cursor while replacement waits = %v, %v; want %v", stored.LastFiredAt, err, firstAt)
	}
	if _, err := store.RequestAppTaskCancellation(ctx, app.AccountID, app.ID, first.ID, secondAt); err != nil {
		t.Fatalf("RequestAppTaskCancellation: %v", err)
	}
	replacement, occurrence, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &firstAt, secondAt,
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || !created || replacement.OccurrenceID != occurrence.ID || occurrence.Status != "queued" {
		t.Fatalf("replacement after confirmed cancellation = %+v, %+v, created=%t, err=%v", replacement, occurrence, created, err)
	}
}

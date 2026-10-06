package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemStoreExclusiveCommandCronAdmissionAndTaskGeneration(t *testing.T) {
	store, ctx, account, app, _ := memCoverageFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store.exclusiveNow = func() time.Time { return now }
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:exclusive-command", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/command", "apps/command.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command:        []string{"bin/synchronize", "--incremental"},
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "skip"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	owners := ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatalf("UpsertExclusiveWorkPolicy: %v", err)
	}
	if _, err := store.UpsertExclusiveTriggerBinding(ctx, ExclusiveTriggerBinding{
		Source: "cron", TriggerID: cron.ID, AccountID: account.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`),
	}); err != nil {
		t.Fatalf("UpsertExclusiveTriggerBinding(command cron): %v", err)
	}
	request, _ := json.Marshal(struct {
		Kind   string `json:"kind"`
		CronID string `json:"cron_id"`
	}{Kind: "command_cron", CronID: cron.ID})
	firedAt := now.Add(time.Minute)
	admission := ExclusiveAdmission{
		AccountID: account.ID, AppID: app.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`), Request: request,
		IdempotencyKey: "command-cron:" + cron.ID + ":" + firedAt.Format(time.RFC3339Nano),
	}
	_, occurrence, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firedAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: cron.ScheduleRevision, ExclusiveAdmission: &admission})
	if err != nil || !created || occurrence.Status != "pending" || occurrence.ExclusiveOperationID == "" || occurrence.AppTaskID != "" {
		t.Fatalf("managed occurrence=%+v created=%t err=%v; want pending operation without an unowned task", occurrence, created, err)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(firedAt) {
		t.Fatalf("cron cursor=%v err=%v; want atomic advance to %v", storedCron.LastFiredAt, err, firedAt)
	}
	op, err := owners.ExclusiveOperationByID(ctx, account.ID, occurrence.ExclusiveOperationID)
	if err != nil || op.State != "pending" {
		t.Fatalf("operation=%+v err=%v; want admitted pending operation", op, err)
	}
	claim, err := owners.ClaimExclusiveOperation(ctx, account.ID, op.ID, "test-command-cron")
	if err != nil || claim.Generation != 1 {
		t.Fatalf("ClaimExclusiveOperation=%+v err=%v; want generation 1", claim, err)
	}
	firstTask, err := store.CreateExclusiveCommandCronAppTask(ctx, account.ID, app.ID, op.ID, claim.Generation, cron.ID, now)
	if err != nil || firstTask.ExclusiveOperationID != op.ID || firstTask.ExclusiveGeneration != claim.Generation ||
		firstTask.OccurrenceID != occurrence.ID || firstTask.ScheduledFor == nil || !firstTask.ScheduledFor.Equal(firedAt) {
		t.Fatalf("first owned task=%+v err=%v; want generation-fenced occurrence task", firstTask, err)
	}
	now = now.Add(6 * time.Second)
	secondClaim, err := owners.ClaimExclusiveOperation(ctx, account.ID, op.ID, "replacement-command-cron")
	if err != nil || secondClaim.Generation != 2 {
		t.Fatalf("replacement claim=%+v err=%v; want generation 2", secondClaim, err)
	}
	if _, err := store.CreateExclusiveCommandCronAppTask(ctx, account.ID, app.ID, op.ID, claim.Generation, cron.ID, now); !errors.Is(err, exclusivework.ErrStaleOwner) {
		t.Fatalf("stale task creation error=%v; want ErrStaleOwner", err)
	}
	secondTask, err := store.CreateExclusiveCommandCronAppTask(ctx, account.ID, app.ID, op.ID, secondClaim.Generation, cron.ID, now)
	if err != nil || secondTask.ExclusiveGeneration != 2 || secondTask.OccurrenceID != occurrence.ID {
		t.Fatalf("replacement task=%+v err=%v; want the newer generation on the same occurrence", secondTask, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 1 || history[0].AppTaskID != secondTask.ID || history[0].ExclusiveOperationID != op.ID {
		t.Fatalf("occurrence history=%+v err=%v; want latest fenced task and operation link", history, err)
	}

	// The cron's legacy overlap=skip setting must not bypass a managed queue
	// policy when the active task already belongs to the same exclusive lane.
	nextScheduledFor := firedAt.Add(time.Minute)
	nextAdmission := admission
	nextAdmission.IdempotencyKey = "command-cron:" + cron.ID + ":" + nextScheduledFor.Format(time.RFC3339Nano)
	_, nextOccurrence, nextCreated, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &firedAt, nextScheduledFor,
		CronScheduledOccurrenceOptions{ScheduledFor: nextScheduledFor, ScheduleRevision: cron.ScheduleRevision, ExclusiveAdmission: &nextAdmission})
	if err != nil || !nextCreated || nextOccurrence.Status != "pending" || nextOccurrence.ExclusiveOperationID == "" || nextOccurrence.ExclusiveOperationID == op.ID {
		t.Fatalf("next managed occurrence=%+v created=%t err=%v; want a queued operation despite active same-cron task", nextOccurrence, nextCreated, err)
	}
}

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

func TestMemStoreHTTPCronAcceptsStructuredFailureRules(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "replace", MissedRuns: "skip"},
	})
	if err != nil || cron.SchedulePolicy == nil || cron.SchedulePolicy.Overlap != "replace" {
		t.Fatalf("CreateCronWithOptions(HTTP with schedule policy) = %+v, %v; want stored policy", cron, err)
	}
	rules := &workpolicy.FailureRules{
		Version:          workpolicy.Version,
		Rules:            []workpolicy.FailureRule{{OutcomeCodes: []string{"invalid_record"}, Action: "fail_partition"}},
		UnmatchedFailure: "retry", UncertainOutcome: "hold",
	}
	classified, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync-failure", true, CronOptions{
		FailureRules: rules,
	})
	if err != nil || classified.FailureRules == nil || classified.FailureRules.Rules[0].OutcomeCodes[0] != "invalid_record" {
		t.Fatalf("CreateCronWithOptions(HTTP with structured failure rules) = %+v, %v", classified, err)
	}
	_, err = store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync-exit-code", true, CronOptions{
		FailureRules: &workpolicy.FailureRules{Version: workpolicy.Version,
			Rules:            []workpolicy.FailureRule{{ExitCodes: []int{65}, Action: "fail_partition"}},
			UnmatchedFailure: "retry", UncertainOutcome: "hold"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateCronWithOptions(HTTP with exit code rule) = %v; want ErrInvalidArgument", err)
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

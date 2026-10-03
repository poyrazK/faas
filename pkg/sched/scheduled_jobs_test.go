// adr: 099
package sched

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunScheduledJobsTickCreatesOneRunForDueOccurrence(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID:      "scheduled-account",
		Name:           "minute-worker",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/worker:v1",
		Command:        []string{"/app/run"},
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 2,
		RetryMax:       1,
		CronSchedule:   "* * * * *",
		CronTimezone:   "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	now := job.CreatedAt.Add(2 * time.Minute).UTC()
	loop := &Loop{
		engine: &Engine{store: store},
		now:    func() time.Time { return now },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	loop.runScheduledJobsTick(ctx)
	loop.runScheduledJobsTick(ctx)
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil {
		t.Fatalf("JobRunListByJob: %v", err)
	}
	if len(runs) != 1 || runs[0].TriggerKind != "scheduled" || runs[0].Tasks != 1 {
		t.Fatalf("scheduled runs = %+v; want exactly one one-task scheduled run", runs)
	}
}

func TestRunScheduledJobsTickDoesNotCatchUpWhilePaused(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID:      "scheduled-account",
		Name:           "minute-worker",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/worker:v1",
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 1,
		CronSchedule:   "* * * * *",
		CronTimezone:   "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	paused := "paused"
	if _, err := store.JobUpdate(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &paused); err != nil {
		t.Fatalf("JobUpdate(pause): %v", err)
	}
	loop := &Loop{
		engine: &Engine{store: store},
		now:    func() time.Time { return job.CreatedAt.Add(24 * time.Hour) },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loop.runScheduledJobsTick(ctx)
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs while paused = %+v, err %v; want none", runs, err)
	}
}

func TestRunScheduledJobsTickAdmitsManagedJobAndSharesLaneWithAppWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drain, base, _, _, _ := newDrainHarness(t, api.PlanPro, true)
	store := base.(*state.MemStore)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAllApps() = %d rows, %v", len(apps), err)
	}
	app := apps[0]
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID: app.AccountID, Name: "nightly-import", Kind: "recurring",
		ImageRef: "ghcr.io/example/importer:v1", Command: []string{"/app/import"},
		RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1,
		CronSchedule: "* * * * *", CronTimezone: "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	owners := state.ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{
		Name: "customer-sync", Scope: "account", MemberAppIDs: []string{app.ID}, MemberJobIDs: []string{job.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 30,
	}); err != nil {
		t.Fatalf("UpsertExclusiveWorkPolicy: %v", err)
	}
	if _, err := store.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "job_schedule", TriggerID: job.ID, AccountID: app.AccountID,
		PolicyName: "customer-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`),
	}); err != nil {
		t.Fatalf("UpsertExclusiveTriggerBinding: %v", err)
	}

	now := job.CreatedAt.Add(2 * time.Minute).UTC()
	loop := &Loop{
		engine: drain.engine,
		now:    func() time.Time { return now },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loop.runScheduledJobsTick(ctx)
	loop.runScheduledJobsTick(ctx)
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatalf("unmanaged scheduled JobRuns = %+v, err %v; want none", runs, err)
	}
	due, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(due) != 1 || due[0].JobID != job.ID {
		t.Fatalf("due managed operations = %+v, err %v; want one Job operation", due, err)
	}
	scheduledOperation := due[0]
	appOperation, _, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
		AccountID: app.AccountID, AppID: app.ID, PolicyName: "customer-sync",
		Key:     json.RawMessage(`"customer:acme:crm-sync"`),
		Request: json.RawMessage(`{"method":"POST","path":"/sync"}`),
	})
	if err != nil || appOperation.ID == scheduledOperation.ID || appOperation.State != "pending" {
		t.Fatalf("app work sharing Job lane = %+v, err %v", appOperation, err)
	}

	drain.tickExclusiveOperations(ctx)
	managedRuns, err := store.JobRunListByExclusiveOperation(ctx, app.AccountID, scheduledOperation.ID)
	if err != nil || len(managedRuns) != 1 {
		t.Fatalf("managed scheduled JobRuns = %+v, err %v", managedRuns, err)
	}
	managedRun := managedRuns[0]
	if managedRun.TriggerKind != "scheduled" || managedRun.ExclusiveGeneration == 0 || managedRun.ExclusiveOperationID != scheduledOperation.ID {
		t.Fatalf("managed scheduled JobRun lost its trigger or owner: %+v", managedRun)
	}
	if err := store.JobTaskMarkTerminal(ctx, managedRun.ID, 0, "succeeded", 0, "", "", time.Now().UTC()); err != nil {
		t.Fatalf("complete managed scheduled task: %v", err)
	}
	if _, err := store.JobRunRecompute(ctx, managedRun.ID); err != nil {
		t.Fatalf("recompute managed scheduled run: %v", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		operation, getErr := owners.ExclusiveOperationByID(ctx, app.AccountID, scheduledOperation.ID)
		if getErr == nil && operation.State == "completed" {
			if operation.Generation != managedRun.ExclusiveGeneration {
				t.Fatalf("operation generation %d != JobRun generation %d", operation.Generation, managedRun.ExclusiveGeneration)
			}
			var result map[string]any
			if err := json.Unmarshal(operation.Result, &result); err != nil || result["job_run_id"] != managedRun.ID {
				t.Fatalf("committed Job result = %s, err %v", operation.Result, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	operation, err := owners.ExclusiveOperationByID(ctx, app.AccountID, scheduledOperation.ID)
	t.Fatalf("scheduled Job operation did not commit: state=%q err=%v", operation.State, err)
}

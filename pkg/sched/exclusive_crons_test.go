package sched

// adr: 387

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestExclusiveCronAndManualFireShareJoinExistingLane(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "exclusive-cron@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, cron := newAppAndCron(t, store, account.ID, true)
	cron, err = store.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := store
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "join_existing",
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	bindings := store
	if _, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "cron", TriggerID: cron.ID, AccountID: account.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync",
	}); err != nil {
		t.Fatal(err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Now().UTC().Truncate(time.Minute)
	loop := NewLoop(nil, engine, slog.Default()).WithClock(func() time.Time { return now })
	loop.dispatchOneCron(ctx, cron, now)
	first, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(first) != 1 || first[0].State != "pending" {
		t.Fatalf("scheduled trigger did not enter the operation lane: rows=%+v err=%v", first, err)
	}
	manual, err := loop.RunCronNow(ctx, cron.ID, account.ID)
	if err != nil || !manual.Success || manual.ExclusiveOperationID != first[0].ID {
		t.Fatalf("manual fire bypassed or failed to join the lane: run=%+v err=%v", manual, err)
	}
	after, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(after) != 1 || after[0].ID != first[0].ID {
		t.Fatalf("equivalent manual request created another operation: rows=%+v err=%v", after, err)
	}
}

func TestExclusiveManualCronReceiptContainsOperationID(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "exclusive-fire-now@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, cron := newAppAndCron(t, store, account.ID, true)
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue",
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "cron", TriggerID: cron.ID, AccountID: account.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`),
	}); err != nil {
		t.Fatal(err)
	}
	requestID, err := store.InsertFireNowRequest(ctx, cron.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.ClaimPendingFireNowRequest(ctx)
	if err != nil || request.ID != requestID {
		t.Fatalf("claim fire-now request: %+v %v", request, err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, engine, slog.Default()).WithClock(func() time.Time { return time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC) })
	loop.processFireNowRequest(ctx, request)
	finished, err := store.GetFireNowRequest(ctx, requestID)
	if err != nil || finished.Status != state.FireNowStatusSucceeded || finished.OperationID == nil || *finished.OperationID == "" || finished.InvocationID != nil {
		t.Fatalf("fire-now operation receipt=%+v err=%v", finished, err)
	}
	if _, err := store.ExclusiveOperationByID(ctx, account.ID, *finished.OperationID); err != nil {
		t.Fatalf("fire-now receipt points to missing operation: %v", err)
	}
}

func TestExclusiveCommandCronScheduleAndFireNowShareFencedTaskLane(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "exclusive-command-cron@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "command-cron", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:command-cron",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/command-cron", "apps/command-cron.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, state.CronOptions{
		Command: []string{"bin/synchronize", "--incremental"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	createdAt := now.Add(-time.Minute)
	cron, err = store.UpdateCron(ctx, cron.ID, nil, nil, nil, &createdAt)
	if err != nil {
		t.Fatal(err)
	}
	owners := state.ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "join_existing",
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "cron", TriggerID: cron.ID, AccountID: account.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync",
	}); err != nil {
		t.Fatalf("bind command cron: %v", err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, engine, slog.Default()).WithClock(func() time.Time { return now })
	loop.dispatchOneCron(ctx, cron, now)
	operations, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(operations) != 1 || operations[0].State != "pending" {
		t.Fatalf("scheduled command cron operations=%+v err=%v; want one pending operation", operations, err)
	}
	occurrences, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(occurrences) != 1 || occurrences[0].ExclusiveOperationID != operations[0].ID || occurrences[0].Status != "pending" {
		t.Fatalf("scheduled occurrences=%+v err=%v; want atomically linked pending operation", occurrences, err)
	}

	requestID, err := store.InsertFireNowRequest(ctx, cron.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.ClaimPendingFireNowRequest(ctx)
	if err != nil || request.ID != requestID {
		t.Fatalf("claim fire-now request=%+v err=%v", request, err)
	}
	loop.processFireNowRequest(ctx, request)
	receipt, err := store.GetFireNowRequest(ctx, requestID)
	if err != nil || receipt.Status != state.FireNowStatusSucceeded || receipt.OperationID == nil || *receipt.OperationID != operations[0].ID {
		t.Fatalf("fire-now receipt=%+v err=%v; want the scheduled operation", receipt, err)
	}
	stillOne, err := owners.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(stillOne) != 1 || stillOne[0].ID != operations[0].ID {
		t.Fatalf("equivalent manual fire created another operation: %+v err=%v", stillOne, err)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	drain := &Drain{store: store, engine: engine, appTasks: &AppTaskCoordinator{}, now: func() time.Time { return now }, log: slog.Default()}
	if outcome := drain.dispatchExclusiveAppTaskOperation(workerCtx, owners, stillOne[0]); outcome != "completed" {
		cancel()
		t.Fatalf("exclusive command cron dispatch outcome=%q; want completed", outcome)
	}
	tasks, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(tasks) != 1 {
		cancel()
		t.Fatalf("command cron tasks=%+v err=%v; want one generation-fenced task", tasks, err)
	}
	task := tasks[0]
	if task.ExclusiveOperationID != operations[0].ID || task.ExclusiveGeneration != 1 || task.OccurrenceID != occurrences[0].ID ||
		task.ScheduledFor == nil || !task.ScheduledFor.Equal(now) || task.DeploymentID != deployment.ID {
		cancel()
		t.Fatalf("managed command task=%+v; want operation generation 1 and scheduled occurrence", task)
	}
	receipt, err = store.GetFireNowRequest(ctx, requestID)
	if err != nil || receipt.TaskID == nil || *receipt.TaskID != task.ID {
		cancel()
		t.Fatalf("linked fire-now receipt=%+v err=%v; want task %s", receipt, err, task.ID)
	}
	cancel()
}

// A bound HTTP cron with a schedule policy goes through the operation lock
// like a plain one. The policy branch used to queue an ordinary cron run and
// bypass the lock (crons audit 2026-11-05).
func TestExclusivePolicyCronGoesThroughTheLock(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "exclusive-policy-cron@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, cron := newAppAndCron(t, store, account.ID, true)
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	if _, err := store.UpdateCronWithOptions(ctx, cron.ID, nil, nil, nil, nil, nil, nil, state.CronOptions{SchedulePolicy: policy}); err != nil {
		t.Fatal(err)
	}
	if cron, err = store.CronByID(ctx, cron.ID); err != nil || cron.SchedulePolicy == nil {
		t.Fatalf("policy cron = %+v err=%v", cron, err)
	}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "join_existing",
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
		Source: "cron", TriggerID: cron.ID, AccountID: account.ID, PolicyName: "crm-sync",
		Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync",
	}); err != nil {
		t.Fatal(err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Now().UTC().Truncate(time.Minute)
	loop := NewLoop(nil, engine, slog.Default()).WithClock(func() time.Time { return now })
	loop.dispatchOneCron(ctx, cron, now)
	ops, err := store.ListDueExclusiveOperations(ctx, 10)
	if err != nil || len(ops) != 1 {
		t.Fatalf("policy cron did not enter the operation lane: rows=%+v err=%v", ops, err)
	}
	if runs, err := store.ListCronRunsForCron(ctx, cron.ID, 10, ""); err != nil || len(runs) != 0 {
		t.Fatalf("policy cron also queued a plain run beside the lock: runs=%d err=%v", len(runs), err)
	}
	if got, _ := store.CronByID(ctx, cron.ID); got.LastFiredAt.IsZero() {
		t.Fatal("cursor not advanced after the operation was admitted")
	}
}

// The tried set belongs to one minute: a pass in a later minute dispatches
// crons the previous minute already tried, even when that minute's tick was
// coalesced away (crons audit 2026-11-05).
func TestCronSweepTriedSetResetsEachMinute(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 10, 0, time.UTC)
	loop := NewLoop(nil, nil, slog.Default()).WithClock(func() time.Time { return now })
	loop.cronSweepTried = map[string]struct{}{"c1": {}}
	loop.cronSweepMinute = now.Truncate(time.Minute)
	if loop.resetCronSweepTriedForMinute(); len(loop.cronSweepTried) != 1 {
		t.Fatal("same-minute continuation dropped the tried set")
	}
	now = now.Add(time.Minute)
	if loop.resetCronSweepTriedForMinute(); len(loop.cronSweepTried) != 0 {
		t.Fatal("next-minute continuation kept the previous minute's tried set")
	}
}

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

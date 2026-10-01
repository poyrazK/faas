package sched

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueuePollerEnvironmentIdentityKeepsConsumersAndLegacyBacklogSeparate(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "scoped-queue-poller@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "scoped-queue-poller"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "scoped-queue-poller", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(scope string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, DeploymentScope: scope, QueueName: "orders", Source: state.InvocationQueue, DueAt: time.Now().Add(-time.Second), Payload: []byte(`{"job":"retained"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	legacy := enqueue("staging")
	create := func(scope string) state.QueueBindingConsumerResult {
		t.Helper()
		result, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: scope, Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	prod, stage := create("production"), create("staging")
	production, staging := enqueue("production"), enqueue("staging")
	prodTrigger, err := store.TriggerByID(ctx, prod.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	stageTrigger, err := store.TriggerByID(ctx, stage.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := newQueuePoller(pool, prodTrigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	prodPoller := source.(*queuePoller)
	source, err = newQueuePoller(pool, stageTrigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	stagePoller := source.(*queuePoller)
	if _, err := store.ClaimQueueTriggerInvocation(ctx, staging.ID, prodTrigger.ID.String(), app.ID, "orders", 600); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production claimed staging: %v", err)
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, legacy.ID, stageTrigger.ID.String(), app.ID, "orders", 600); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("scoped consumer adopted old name-based backlog: %v", err)
	}
	for _, item := range []struct {
		poller *queuePoller
		scope  string
		inv    state.Invocation
	}{{prodPoller, "production", production}, {stagePoller, "staging", staging}} {
		trigger := prodTrigger
		if item.scope == "staging" {
			trigger = stageTrigger
		}
		got := item.poller.Poll(ctx, trigger)
		if got.Error != nil || len(got.Records) != 1 || got.Records[0].InvocationID != item.inv.ID {
			t.Fatalf("scope %s poll=%+v", item.scope, got)
		}
	}
	if got := stagePoller.Poll(ctx, stageTrigger); got.Error != nil || len(got.Records) != 0 {
		t.Fatalf("binding cap or legacy scope bypassed: %+v", got)
	}
	receipt, err := store.InsertTriggerRecord(ctx, stageTrigger.ID.String(), staging.ID, staging.Payload, []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := stagePoller.Nack(ctx, stageTrigger, []string{staging.ID}, triggerReasonMaxAttempts); err != nil {
		t.Fatal(err)
	}
	name := "payments"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, stage.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.RetryQueueDeadLetter(ctx, account.ID, staging.ID)
	if err != nil || replay.QueueBindingID != stage.Binding.ID || replay.DeploymentScope != "staging" {
		t.Fatalf("replay changed environment identity: %+v %v", replay, err)
	}
	if got := stagePoller.Poll(ctx, stageTrigger); got.Error != nil || len(got.Records) != 0 {
		t.Fatalf("cached renamed consumer claimed: %+v", got)
	}
	stageTrigger, err = store.TriggerByID(ctx, stageTrigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	got := stagePoller.Poll(ctx, stageTrigger)
	if got.Error != nil || len(got.Records) != 1 || got.Records[0].InvocationID != staging.ID || got.Records[0].InvocationReplayGeneration != 1 {
		t.Fatalf("renamed replay poll=%+v", got)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, stageTrigger.ID.String(), staging.ID); err != nil || id != receipt {
		t.Fatal("scoped replay replaced receipt")
	}
	if err := stagePoller.Ack(ctx, stageTrigger, []string{staging.ID}); err != nil {
		t.Fatal(err)
	}
	if row, err := store.InvocationByID(ctx, production.ID); err != nil || row.State != state.InvocationDispatching {
		t.Fatalf("staging recovery altered production: %+v %v", row, err)
	}
	if row, err := store.InvocationByID(ctx, legacy.ID); err != nil || row.State != state.InvocationPending || row.QueueBindingID != "" {
		t.Fatalf("legacy work silently adopted: %+v %v", row, err)
	}
	if err := prodPoller.Ack(ctx, prodTrigger, []string{production.ID}); err != nil {
		t.Fatal(err)
	}
}

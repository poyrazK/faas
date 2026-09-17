package sched

// adr: 100

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueuePollerLinksTriggerAndInvocationOutcomes(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-poller@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "queue-poller",
		Type:      state.AppTypeApp,
		RAMMB:     512,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)

	for _, tc := range []struct {
		name   string
		source state.InvocationSource
		slug   string
	}{
		{name: "queue", source: state.InvocationQueue, slug: "jobs"},
		{name: "delayed task", source: state.InvocationDelayedTask, slug: "timers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", tc.slug, string(tc.source), true,
				[]byte(`{"mode":"`+string(tc.source)+`"}`), 10, 20, 3, 1<<20, "commit", limits)
			if err != nil {
				t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
			}
			pollerSource, err := newQueuePoller(pool, trigger)
			if err != nil {
				t.Fatalf("newQueuePoller: %v", err)
			}
			poller := pollerSource.(*queuePoller)

			invocation, err := store.EnqueueInvocation(ctx, state.Invocation{
				AccountID: account.ID,
				AppID:     app.ID,
				Source:    tc.source,
				Payload:   json.RawMessage(`{"job":"success"}`),
				DueAt:     time.Now().Add(-time.Second),
			})
			if err != nil {
				t.Fatalf("EnqueueInvocation success: %v", err)
			}
			result := poller.Poll(ctx, trigger)
			if result.Error != nil || len(result.Records) != 1 {
				t.Fatalf("Poll success records=%d err=%v", len(result.Records), result.Error)
			}
			recordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), invocation.ID, invocation.Payload, []byte(`{}`), []byte(`{}`))
			if err != nil {
				t.Fatalf("InsertTriggerRecord success: %v", err)
			}
			if err := poller.Ack(ctx, trigger, []string{invocation.ID}); err != nil {
				t.Fatalf("Ack: %v", err)
			}
			assertQueueLinkedStates(t, ctx, pool, invocation.ID, recordID, "completed", "succeeded", "success")

			failed, err := store.EnqueueInvocation(ctx, state.Invocation{
				AccountID: account.ID,
				AppID:     app.ID,
				Source:    tc.source,
				Payload:   json.RawMessage(`{"job":"failed"}`),
				DueAt:     time.Now().Add(-time.Second),
			})
			if err != nil {
				t.Fatalf("EnqueueInvocation failure: %v", err)
			}
			failedRecordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), failed.ID, failed.Payload, []byte(`{}`), []byte(`{}`))
			if err != nil {
				t.Fatalf("InsertTriggerRecord failure: %v", err)
			}
			if err := poller.Nack(ctx, trigger, []string{failed.ID}, triggerReasonPoisonRecord); err != nil {
				t.Fatalf("Nack: %v", err)
			}
			assertQueueLinkedStates(t, ctx, pool, failed.ID, failedRecordID, "dead_letter", "dead_letter", "dead_letter")
		})
	}
}

func assertQueueLinkedStates(t *testing.T, ctx context.Context, pool *pgxpool.Pool, invocationID, recordID, wantInvocation, wantRecord, wantOutcome string) {
	t.Helper()
	var invocationState, outcome, recordState string
	if err := pool.QueryRow(ctx, `
		select i.state, coalesce(i.outcome, ''), tr.state
		  from invocations i
		  join trigger_records tr on tr.id = $2
		 where i.id = $1`, invocationID, recordID).Scan(&invocationState, &outcome, &recordState); err != nil {
		t.Fatalf("read linked states: %v", err)
	}
	if invocationState != wantInvocation || recordState != wantRecord || outcome != wantOutcome {
		t.Fatalf("linked states=(invocation=%q record=%q outcome=%q), want (%q,%q,%q)",
			invocationState, recordState, outcome, wantInvocation, wantRecord, wantOutcome)
	}
}

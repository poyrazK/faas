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
			queueName := ""
			if tc.source == state.InvocationQueue {
				queueName = tc.slug
			}
			if tc.source == state.InvocationQueue {
				if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{
					AccountID: account.ID, AppID: app.ID, Name: tc.slug, QueueName: tc.slug,
					Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
				}); err != nil {
					t.Fatalf("CreateQueueBinding: %v", err)
				}
			}
			trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", tc.slug, true,
				[]byte(`{"mode":"`+string(tc.source)+`"}`), string(tc.source), 10, 20, 3, 1<<20, "commit", limits)
			if err != nil {
				t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
			}
			pollerSource, err := newQueuePoller(pool, trigger, nil)
			if err != nil {
				t.Fatalf("newQueuePoller: %v", err)
			}
			poller := pollerSource.(*queuePoller)

			invocation, err := store.EnqueueInvocation(ctx, state.Invocation{
				AccountID: account.ID,
				AppID:     app.ID,
				Source:    tc.source,
				QueueName: queueName,
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
			var blocked state.Invocation
			if tc.source == state.InvocationQueue {
				blocked, err = store.EnqueueInvocation(ctx, state.Invocation{
					AccountID: account.ID,
					AppID:     app.ID,
					Source:    tc.source,
					QueueName: tc.slug,
					Payload:   json.RawMessage(`{"job":"blocked"}`),
					DueAt:     time.Now().Add(-time.Second),
				})
				if err != nil {
					t.Fatalf("EnqueueInvocation blocked: %v", err)
				}
				throttled := poller.Poll(ctx, trigger)
				if throttled.Error != nil || len(throttled.Records) != 0 {
					t.Fatalf("Poll while binding is full records=%d err=%v, want no records", len(throttled.Records), throttled.Error)
				}
			}
			recordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), invocation.ID, invocation.Payload, []byte(`{}`), []byte(`{}`))
			if err != nil {
				t.Fatalf("InsertTriggerRecord success: %v", err)
			}
			if err := poller.Ack(ctx, trigger, []string{invocation.ID}); err != nil {
				t.Fatalf("Ack: %v", err)
			}
			assertQueueLinkedStates(t, ctx, pool, invocation.ID, recordID, "completed", "succeeded", "success")
			if tc.source == state.InvocationQueue {
				released := poller.Poll(ctx, trigger)
				if released.Error != nil || len(released.Records) != 1 || released.Records[0].ItemIdentifier != blocked.ID {
					t.Fatalf("Poll after binding slot release record=%+v err=%v", released.Records, released.Error)
				}
				blockedRecordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), blocked.ID, blocked.Payload, []byte(`{}`), []byte(`{}`))
				if err != nil {
					t.Fatalf("InsertTriggerRecord blocked: %v", err)
				}
				if err := poller.Ack(ctx, trigger, []string{blocked.ID}); err != nil {
					t.Fatalf("Ack blocked: %v", err)
				}
				assertQueueLinkedStates(t, ctx, pool, blocked.ID, blockedRecordID, "completed", "succeeded", "success")
			}

			failed, err := store.EnqueueInvocation(ctx, state.Invocation{
				AccountID: account.ID,
				AppID:     app.ID,
				Source:    tc.source,
				QueueName: queueName,
				Payload:   json.RawMessage(`{"job":"failed"}`),
				DueAt:     time.Now().Add(-time.Second),
			})
			if err != nil {
				t.Fatalf("EnqueueInvocation failure: %v", err)
			}
			failedPoll := poller.Poll(ctx, trigger)
			if failedPoll.Error != nil || len(failedPoll.Records) != 1 || failedPoll.Records[0].ItemIdentifier != failed.ID {
				t.Fatalf("Poll failure record=%+v err=%v", failedPoll.Records, failedPoll.Error)
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

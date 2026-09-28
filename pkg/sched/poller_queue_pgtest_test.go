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
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
			keyed, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
				AccountID: account.ID, AppID: app.ID, Source: tc.source, QueueName: queueName,
				Payload: json.RawMessage(`{"job":"keyed"}`), DueAt: time.Now().Add(-time.Second),
			}, workpolicy.Policy{Name: "policy-" + tc.slug, MaxRunningPerKey: 1}, "s:job-1")
			if err != nil {
				t.Fatalf("EnqueueKeyedInvocation: %v", err)
			}
			if result := poller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 0 {
				t.Fatalf("poller claimed keyed row: %+v", result)
			}
			due, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range due {
				found = found || row.ID == keyed.ID
			}
			if !found {
				t.Fatal("keyed row with queue trigger was not offered to the keyed drain")
			}

			// A lost queue dispatcher leaves both an invocation lease and a
			// trigger-record claim. Once recovered, the next poll must be
			// able to claim the expired record instead of stranding the row.
			recoverable, err := store.EnqueueInvocation(ctx, state.Invocation{
				AccountID: account.ID, AppID: app.ID, Source: tc.source,
				QueueName: queueName, Payload: json.RawMessage(`{"job":"recover"}`),
				DueAt: time.Now().Add(-time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			firstPoll := poller.Poll(ctx, trigger)
			if firstPoll.Error != nil || len(firstPoll.Records) != 1 || firstPoll.Records[0].ItemIdentifier != recoverable.ID {
				t.Fatalf("first recovery poll = %+v", firstPoll)
			}
			if _, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), recoverable.ID, recoverable.Payload, nil, nil); err != nil {
				t.Fatal(err)
			}
			firstClaim, err := store.ClaimTriggerRecords(ctx, trigger.ID.String(), 1)
			if err != nil || len(firstClaim) != 1 || firstClaim[0].ClaimGeneration != 1 {
				t.Fatalf("first recovery claim = %+v, err=%v", firstClaim, err)
			}
			if _, err := pool.Exec(ctx, `update invocations set state='pending', lease_expires_at=null where id=$1`, recoverable.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `update trigger_records set claim_expires_at=now()-interval '1 second' where id=$1`, firstClaim[0].ID); err != nil {
				t.Fatal(err)
			}
			secondPoll := poller.Poll(ctx, trigger)
			if secondPoll.Error != nil || len(secondPoll.Records) != 1 || secondPoll.Records[0].ItemIdentifier != recoverable.ID {
				t.Fatalf("expired claim poll = %+v", secondPoll)
			}
			secondClaim, err := store.ClaimTriggerRecords(ctx, trigger.ID.String(), 1)
			if err != nil || len(secondClaim) != 1 || secondClaim[0].ClaimGeneration != 2 {
				t.Fatalf("recovered claim = %+v, err=%v", secondClaim, err)
			}
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

package sched

// adr: 100

import (
	"context"
	"encoding/json"
	"errors"
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
			keyedPoll := poller.Poll(ctx, trigger)
			if keyedPoll.Error != nil {
				t.Fatal(keyedPoll.Error)
			}
			due, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range due {
				found = found || row.ID == keyed.ID
			}
			if tc.source == state.InvocationQueue {
				if len(keyedPoll.Records) != 1 || keyedPoll.Records[0].ItemIdentifier != keyed.ID || found {
					t.Fatalf("named keyed queue ownership: polled=%+v drain_found=%v", keyedPoll, found)
				}
				if _, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), keyed.ID, keyed.Payload, nil, nil); err != nil {
					t.Fatal(err)
				}
				if err := poller.Ack(ctx, trigger, []string{keyed.ID}); err != nil {
					t.Fatal(err)
				}
				unnamed, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
					AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
					Payload: json.RawMessage(`{"job":"unnamed-keyed"}`),
				}, workpolicy.Policy{Name: "unnamed", MaxRunningPerKey: 1}, "s:job-2")
				if err != nil {
					t.Fatal(err)
				}
				if result := poller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 0 {
					t.Fatalf("named consumer took over unnamed keyed work: %+v", result)
				}
				genericDue, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64)
				if err != nil {
					t.Fatal(err)
				}
				foundUnnamed := false
				for _, row := range genericDue {
					foundUnnamed = foundUnnamed || row.ID == unnamed.ID
				}
				if !foundUnnamed {
					t.Fatal("unnamed keyed work was not offered to the generic drain")
				}
			} else if len(keyedPoll.Records) != 0 || !found {
				t.Fatalf("delayed keyed task ownership: polled=%+v drain_found=%v", keyedPoll, found)
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

func TestNamedQueuePollerSharesWorkReservationsAndFencesAcknowledgement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-work-lane-"+time.Now().Format("150405.000000000")+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-work-lanes", Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 3,
	}); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "jobs", true,
		[]byte(`{"mode":"queue"}`), "queue", 10, 20, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	newPoller := func() *queuePoller {
		t.Helper()
		source, err := newQueuePoller(pool, trigger, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source.(*queuePoller)
	}
	policy := workpolicy.Policy{Name: "documents", MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 1}
	add := func(key, fairness string) state.Invocation {
		t.Helper()
		row, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
			AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
			QueueName: "jobs", Payload: json.RawMessage(`{"job":"index"}`),
		}, policy, key, fairness)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first := add("s:doc-1", "s:tenant-a")
	second := add("s:doc-1", "s:tenant-a")
	third := add("s:doc-2", "s:tenant-a")
	fourth := add("s:doc-3", "s:tenant-b")
	poller := newPoller()
	initial := poller.Poll(ctx, trigger)
	if initial.Error != nil || len(initial.Records) != 2 ||
		initial.Records[0].ItemIdentifier != first.ID || initial.Records[1].ItemIdentifier != fourth.ID {
		t.Fatalf("initial work reservation = %+v", initial)
	}
	if due, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64); err != nil || len(due) != 0 {
		t.Fatalf("named queue leaked to generic drain: %+v, err=%v", due, err)
	}
	crossSource, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AccountID: account.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke,
		Payload: json.RawMessage(`{"job":"cross-source"}`),
	}, policy, "s:doc-1", "s:tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, crossSource.ID, "", 600, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic dispatcher claimed queue-owned lane: %v", err)
	}
	if err := poller.Ack(ctx, trigger, []string{first.ID}); err != nil {
		t.Fatal(err)
	}
	next := poller.Poll(ctx, trigger)
	if next.Error != nil || len(next.Records) != 1 || next.Records[0].ItemIdentifier != second.ID {
		t.Fatalf("next work reservation = %+v", next)
	}
	if err := poller.Ack(ctx, trigger, []string{second.ID}); err != nil {
		t.Fatal(err)
	}
	next = poller.Poll(ctx, trigger)
	if next.Error != nil || len(next.Records) != 1 || next.Records[0].ItemIdentifier != third.ID {
		t.Fatalf("fairness release = %+v", next)
	}
	for _, row := range []state.Invocation{third, fourth} {
		if err := poller.Ack(ctx, trigger, []string{row.ID}); err != nil {
			t.Fatal(err)
		}
	}
	claimedCrossSource, err := store.ClaimInvocationWithCap(ctx, crossSource.ID, "", 600, 10)
	if err != nil || claimedCrossSource.State != state.InvocationDispatching {
		t.Fatalf("cross-source lane did not release: %+v, err=%v", claimedCrossSource, err)
	}
	if err := store.CompleteKeyedInvocation(ctx, claimedCrossSource.ID, claimedCrossSource.Attempts, nil); err != nil {
		t.Fatal(err)
	}
	stale := add("s:doc-4", "s:tenant-c")
	oldPoller := newPoller()
	if result := oldPoller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 1 {
		t.Fatalf("first claim for fencing = %+v", result)
	}
	if _, err := pool.Exec(ctx, `update invocations set state='pending', lease_expires_at=null where id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	newOwner := newPoller()
	if result := newOwner.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 1 {
		t.Fatalf("replacement claim for fencing = %+v", result)
	}
	if err := oldPoller.Ack(ctx, trigger, []string{stale.ID}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.InvocationByID(ctx, stale.ID)
	if err != nil || claimed.State != state.InvocationDispatching || claimed.Attempts != 2 {
		t.Fatalf("stale acknowledgement changed new claim: %+v, err=%v", claimed, err)
	}
	if err := newOwner.Ack(ctx, trigger, []string{stale.ID}); err != nil {
		t.Fatal(err)
	}
	retry := add("s:retry", "s:tenant-retry")
	retryPoller := newPoller()
	if result := retryPoller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 1 || result.Records[0].ItemIdentifier != retry.ID {
		t.Fatalf("first keyed retry claim = %+v", result)
	}
	if err := retryPoller.Nack(ctx, trigger, []string{retry.ID}, triggerReasonBrokerError); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update invocations set due_at=now()-interval '1 second' where id=$1`, retry.ID); err != nil {
		t.Fatal(err)
	}
	if result := retryPoller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 1 || result.Records[0].ItemIdentifier != retry.ID {
		t.Fatalf("keyed retry was not released: %+v", result)
	}
	if err := retryPoller.Ack(ctx, trigger, []string{retry.ID}); err != nil {
		t.Fatal(err)
	}
	// The candidate window is bounded. A large FIFO lane must contribute
	// only its runnable head so a later independent key still reaches it.
	var head state.Invocation
	for i := 0; i < 1025; i++ {
		row := add("s:large-backlog", "s:tenant-large")
		if i == 0 {
			head = row
		}
	}
	independent := add("s:late-key", "s:tenant-late")
	limited := trigger
	limited.BatchSizeMax = 1
	firstBatch := newPoller().Poll(ctx, limited)
	if firstBatch.Error != nil || len(firstBatch.Records) != 1 || firstBatch.Records[0].ItemIdentifier != head.ID {
		t.Fatalf("named poller overclaimed batch: %+v", firstBatch)
	}
	secondBatch := newPoller().Poll(ctx, trigger)
	if secondBatch.Error != nil || len(secondBatch.Records) != 1 || secondBatch.Records[0].ItemIdentifier != independent.ID {
		t.Fatalf("large lane blocked independent work: %+v", secondBatch)
	}
}

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueReplayRetainsFailedEventScope(t *testing.T) {
	for _, kind := range []string{"queue", "nats"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			pool := pgtest.Open(t)
			if err := db.MigrateUp(ctx, pool); err != nil {
				t.Fatal(err)
			}
			store := state.NewPgStore(pool)
			account, err := store.CreateAccount(ctx, "failed-event-scope@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			apps := make([]state.App, 0, 2)
			receipts := make([]string, 0, 2)
			for _, slug := range []string{"first", "neighbor"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
				if err != nil {
					t.Fatal(err)
				}
				var triggerID, item string
				if kind == "queue" {
					binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
					if err != nil {
						t.Fatal(err)
					}
					triggerID = binding.Changes[0].TriggerID
					inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: "jobs", DueAt: time.Now()})
					if err != nil {
						t.Fatal(err)
					}
					item = inv.ID
					if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 60, 10); err != nil {
						t.Fatal(err)
					}
					if err := store.FailInvocation(ctx, inv.ID, "exhausted", time.Nanosecond, 1); err != nil {
						t.Fatal(err)
					}
				} else {
					trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "nats", "events", true, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro))
					if err != nil {
						t.Fatal(err)
					}
					triggerID, item = trigger.ID.String(), "event"
				}
				receipt, err := store.InsertTriggerRecord(ctx, triggerID, item, []byte(`{}`), []byte(`{}`), []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				claims, err := store.ClaimTriggerRecordsByItems(ctx, triggerID, []string{item})
				if err != nil || len(claims) != 1 {
					t.Fatalf("receipt claim=%+v %v", claims, err)
				}
				if err := store.RouteClaimedTriggerDeadLetter(ctx, receipt, claims[0].ClaimGeneration, triggerID, "poison_record", []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
				apps, receipts = append(apps, app), append(receipts, receipt)
			}
			var eventID string
			if err := pool.QueryRow(ctx, `select id::text from dead_letter_events where source='trigger_record' and source_id=$1`, receipts[0]).Scan(&eventID); err != nil {
				t.Fatal(err)
			}
			// Even a mismatched projection cannot redirect an app-scoped replay.
			if _, err := pool.Exec(ctx, `delete from dead_letter_events where source='trigger_record' and source_id=$1`, receipts[1]); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `update dead_letter_events set source_id=$2 where id=$1`, eventID, receipts[1]); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReplayDeadLetterEvent(ctx, account.ID, apps[0].ID, eventID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replay escaped original app=%v", err)
			}
			var status string
			if err := pool.QueryRow(ctx, `select state from trigger_records where id=$1`, receipts[1]).Scan(&status); err != nil || status != "dead_letter" {
				t.Fatalf("neighbor receipt changed=%q %v", status, err)
			}
		})
	}
}

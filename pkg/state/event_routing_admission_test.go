package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type routingAdmissionTestStore interface {
	recipientClaimTestStore
	state.PublishedEventRecipientAdmissionStore
	state.AppWorkPolicyStore
	state.EventWorkBindingStore
}

func forRoutingAdmissionStores(t *testing.T, test func(*testing.T, routingAdmissionTestStore, *pgxpool.Pool, bool)) {
	for _, adopted := range []bool{false, true} {
		t.Run(fmt.Sprintf("independent=%t", adopted), func(t *testing.T) {
			forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, pool *pgxpool.Pool) {
				test(t, store.(routingAdmissionTestStore), pool, adopted)
			})
		})
	}
}

func seedRoutingAdmission(t *testing.T, store routingAdmissionTestStore, adopted bool, action string) (state.PublishedEventRoutingClaim, *state.PublishedEventWork, workpolicy.Policy) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "handoff-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountUUID, err := uuid.Parse(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountUUID.String(), Slug: "handoff-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, accountUUID.String(), app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	if action != "" {
		if _, err := store.UpsertAppWorkPolicy(ctx, accountUUID.String(), app.ID, policy); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventWorkBinding(ctx, app.ID, sub.ID, policy.Name, "data.order_id", state.EventWorkBindingOptions{Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(accountUUID.String(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	accountID := accountUUID.String()
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	claim := state.PublishedEventRoutingClaim{OutboxID: receipt.ID, SubscriptionID: sub.ID, ClaimToken: receipt.ClaimToken}
	if adopted {
		if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		claim.ClaimToken, claim.Generation = work.ClaimToken, work.Generation
	}
	return claim, receipt, policy
}

// ADR-613: admission, checkpoint, history and aggregate settlement commit
// together. Duplicate recovery remains inert after invocation pruning.
func TestEventRoutingAdmissionConcurrentRecoveryAndPruning(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		for _, action := range []string{"", state.EventWorkInvoke} {
			t.Run("action="+action, func(t *testing.T) {
				claim, receipt, _ := seedRoutingAdmission(t, store, adopted, action)
				ctx := context.Background()
				results := make(chan state.PublishedEventRoutingResult, 8)
				errs := make(chan error, 8)
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, err := store.AdmitPublishedEventRecipient(ctx, claim)
						results <- r
						errs <- err
					}()
				}
				wg.Wait()
				close(results)
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				created := 0
				var delivery state.PublishedEventRoutingResult
				for r := range results {
					delivery = r
					if r.InvocationCreated {
						created++
					}
					if !r.ReceiptSettled || r.Progress.State != state.PublishedEventRecipientEnqueued {
						t.Fatalf("result=%+v", r)
					}
				}
				if created != 1 {
					t.Fatalf("created=%d", created)
				}
				inv, err := store.InvocationByID(ctx, delivery.DeliveryID)
				if err != nil {
					t.Fatal(err)
				}
				if action != "" && inv.WorkSequence != 1 {
					t.Fatalf("sequence=%d", inv.WorkSequence)
				}
				history, err := store.ListEventFanoutAttemptsForApp(ctx, inv.AppID, 20, state.EventFanoutAttemptCursor{}, "orders", "", claim.SubscriptionID)
				if err != nil || len(history) != 1 {
					t.Fatalf("history=%+v, %v", history, err)
				}
				if _, err := store.DeleteInvocationsByIDs(ctx, []string{delivery.DeliveryID}); err != nil {
					t.Fatal(err)
				}
				again, err := store.AdmitPublishedEventRecipient(ctx, claim)
				if err != nil || again.InvocationCreated || again.Progress != delivery.Progress {
					t.Fatalf("pruned recovery=%+v,%v", again, err)
				}
				if _, err := store.InvocationByID(ctx, delivery.DeliveryID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("pruned invocation recreated: %v", err)
				}
				if _, err := store.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("settled receipt reclaimed: %+v,%v", receipt, err)
				}
			})
		}
	})
}

func TestEventRoutingAdmissionCancellationDoesNotCancelLaterWork(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, pool *pgxpool.Pool, adopted bool) {
		claim, receipt, policy := seedRoutingAdmission(t, store, adopted, state.EventWorkCancelPending)
		ctx := context.Background()
		recipient := receipt.RecipientSnapshot[0]
		newPending := func() state.Invocation {
			inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: recipient.AppID, AccountID: recipient.AccountID, Source: state.InvocationAsyncInvoke, Payload: []byte(`{}`)}, policy, "s:o-1")
			if err != nil {
				t.Fatal(err)
			}
			return inv
		}
		old := newPending()
		result, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || result.InvocationCreated || !result.ReceiptSettled {
			t.Fatalf("cancellation=%+v,%v", result, err)
		}
		cancellation, err := store.WorkCancellationByID(ctx, result.DeliveryID)
		if err != nil || cancellation.CancelledCount != 1 {
			t.Fatalf("cancellation receipt=%+v,%v", cancellation, err)
		}
		old, err = store.InvocationByID(ctx, old.ID)
		if err != nil || old.State != state.InvocationCancelled {
			t.Fatalf("old=%+v,%v", old, err)
		}
		if pool != nil {
			if _, err := pool.Exec(ctx, `DELETE FROM invocation_work_cancellations WHERE id=$1`, result.DeliveryID); err != nil {
				t.Fatal(err)
			}
		}
		later := newPending()
		if _, err := store.AdmitPublishedEventRecipient(ctx, claim); err != nil {
			t.Fatal(err)
		}
		later, err = store.InvocationByID(ctx, later.ID)
		if err != nil || later.State != state.InvocationPending {
			t.Fatalf("later work cancelled=%+v,%v", later, err)
		}
	})
}

func TestEventRoutingAdmissionUnknownResponsePreservesCheckpoint(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, base recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, _, _, receipt := seedRecipientClaims(t, base)
		store := base.(routingAdmissionTestStore)
		claim := state.PublishedEventRoutingClaim{OutboxID: receipt.ID, SubscriptionID: receipt.RecipientSnapshot[0].ID, ClaimToken: receipt.ClaimToken}
		result, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || result.ReceiptSettled {
			t.Fatalf("first admission=%+v,%v", result, err)
		}
		// Simulate a lost response: the scheduler reports a retry under the still
		// live parent lease while siblings remain. It must not erase success.
		err = store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, claim.SubscriptionID,
			state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientPending, Attempts: 1, UpdatedAt: time.Now().UTC(), LastError: "unknown commit response"})
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("success overwritten: %v", err)
		}
		if _, err := store.DeleteInvocationsByIDs(ctx, []string{result.DeliveryID}); err != nil {
			t.Fatal(err)
		}
		again, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || again.InvocationCreated || again.Progress != result.Progress {
			t.Fatalf("unknown response recovery=%+v,%v", again, err)
		}
	})
}

func TestEventRoutingAdmissionFencesWrongTokenAndGeneration(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		for _, action := range []string{"", state.EventWorkInvoke, state.EventWorkCancelPending} {
			t.Run("action="+action, func(t *testing.T) {
				claim, receipt, _ := seedRoutingAdmission(t, store, adopted, action)
				ctx := context.Background()
				bad := claim
				bad.ClaimToken = uuid.NewString()
				if _, err := store.AdmitPublishedEventRecipient(ctx, bad); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("wrong token=%v", err)
				}
				bad = claim
				bad.Generation++
				if _, err := store.AdmitPublishedEventRecipient(ctx, bad); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("wrong generation=%v", err)
				}
				rows, err := store.ListInvocationsForApp(ctx, receipt.RecipientSnapshot[0].AppID)
				if err != nil || len(rows) != 0 {
					t.Fatalf("stale worker effects=%+v,%v", rows, err)
				}
				if _, err := store.AdmitPublishedEventRecipient(ctx, claim); err != nil {
					t.Fatal(err)
				}
				if adopted {
					bad = claim
					bad.Generation++
					if _, err := store.AdmitPublishedEventRecipient(ctx, bad); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("wrong generation duplicate=%v", err)
					}
				}
			})
		}
	})
}

func TestEventRoutingAdmissionRejectsDeletedTarget(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		claim, receipt, _ := seedRoutingAdmission(t, store, adopted, "")
		ctx := context.Background()
		if _, err := store.SoftDeleteAppCascade(ctx, receipt.RecipientSnapshot[0].AppID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AdmitPublishedEventRecipient(ctx, claim); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("deleted target admission=%v", err)
		}
	})
}

func TestPgEventRoutingAdmissionHistoryFailureRollsBack(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		for _, action := range []string{"", state.EventWorkInvoke, state.EventWorkCancelPending} {
			t.Run(fmt.Sprintf("independent=%t/action=%s", adopted, action), func(t *testing.T) {
				pg, pool, _ := pgStoreWithPool(t)
				store := routingAdmissionTestStore(pg)
				claim, receipt, policy := seedRoutingAdmission(t, store, adopted, action)
				ctx := context.Background()
				recipient := receipt.RecipientSnapshot[0]
				var old state.Invocation
				if action != "" {
					var err error
					old, err = store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: recipient.AppID, AccountID: recipient.AccountID, Source: state.InvocationAsyncInvoke, Payload: []byte(`{}`)}, policy, "s:o-1")
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_routing_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected history failure'; END $$; CREATE TRIGGER reject_routing_history BEFORE INSERT ON event_fanout_attempt_history FOR EACH ROW EXECUTE FUNCTION reject_routing_history()`); err != nil {
					t.Fatal(err)
				}
				result, err := store.AdmitPublishedEventRecipient(ctx, claim)
				if err == nil {
					t.Fatal("history failure accepted")
				}
				if !result.Matched {
					t.Fatal("storage failure lost matched classification")
				}
				rows, err := store.ListInvocationsForApp(ctx, recipient.AppID)
				want := 0
				if action != "" {
					want = 1
				}
				if err != nil || len(rows) != want {
					t.Fatalf("partial invocations=%+v,%v", rows, err)
				}
				if action != "" {
					old, err = store.InvocationByID(ctx, old.ID)
					if err != nil || old.State != state.InvocationPending {
						t.Fatalf("partial lane mutation=%+v,%v", old, err)
					}
				}
				var receipts int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM invocation_work_cancellations`).Scan(&receipts); err != nil || receipts != 0 {
					t.Fatalf("partial cancellations=%d,%v", receipts, err)
				}
				var history, checkpoints int
				var parentState string
				if err := pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM jsonb_object_keys(recipient_progress)),
				  (SELECT count(*) FROM event_fanout_attempt_history WHERE outbox_id=$1)
				  FROM event_fanout_outbox WHERE id=$1`, claim.OutboxID).Scan(&parentState, &checkpoints, &history); err != nil || parentState != "processing" || checkpoints != 0 || history != 0 {
					t.Fatalf("partial checkpoint/settlement: state=%s checkpoints=%d history=%d err=%v", parentState, checkpoints, history, err)
				}
				if _, err := pool.Exec(ctx, `DROP TRIGGER reject_routing_history ON event_fanout_attempt_history`); err != nil {
					t.Fatal(err)
				}
				result, err = store.AdmitPublishedEventRecipient(ctx, claim)
				if err != nil || !result.ReceiptSettled {
					t.Fatalf("recovered admission=%+v,%v", result, err)
				}
				if action == state.EventWorkInvoke {
					inv, err := store.InvocationByID(ctx, result.DeliveryID)
					if err != nil || inv.WorkSequence != 2 {
						t.Fatalf("rolled back lane sequence=%+v,%v", inv, err)
					}
				}
			})
		}
	}
}

// An expiry caused by a slow history write must roll back mutations already
// applied to both invocation and broker work in the shared lane.
func TestPgEventRoutingAdmissionExpiryRollsBackSharedLane(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		for _, action := range []string{state.EventWorkInvoke, state.EventWorkCancelPending} {
			t.Run(fmt.Sprintf("independent=%t/action=%s", adopted, action), func(t *testing.T) {
				pg, pool, ctx := pgStoreWithPool(t)
				claim, receipt, _ := seedRoutingAdmission(t, pg, adopted, action)
				recipient := receipt.RecipientSnapshot[0]
				policy, err := pg.AppWorkPolicyByName(ctx, recipient.AppID, "orders")
				if err != nil {
					t.Fatal(err)
				}
				trigger, err := pg.CreateTriggerIfUnderQuota(ctx, recipient.AppID, "kafka", "orders", true, []byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
				if err != nil {
					t.Fatal(err)
				}
				brokerID, err := pg.InsertKeyedTriggerRecord(ctx, trigger.ID.String(), "offset-1", []byte(`{}`), nil, nil, policy, "s:o-1")
				if err != nil {
					t.Fatal(err)
				}
				// Use a preserve-all explicit writer so both pending rows occupy the lane.
				preserve := policy.Policy
				preserve.PendingUpdates = workpolicy.PendingAll
				old, err := pg.EnqueueKeyedInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: recipient.AppID, AccountID: recipient.AccountID, Source: state.InvocationAsyncInvoke, Payload: []byte(`{}`)}, preserve, "s:o-1")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `CREATE FUNCTION slow_routing_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.3); RETURN NEW; END $$; CREATE TRIGGER slow_routing_history BEFORE INSERT ON event_fanout_attempt_history FOR EACH ROW EXECUTE FUNCTION slow_routing_history()`); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE event_fanout_outbox SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE id=$1`
				if adopted {
					query = `UPDATE event_fanout_recipients SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE outbox_id=$1`
				}
				if _, err := pool.Exec(ctx, query, claim.OutboxID); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.AdmitPublishedEventRecipient(ctx, claim); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("expired history commit=%v", err)
				}
				old, err = pg.InvocationByID(ctx, old.ID)
				if err != nil || old.State != state.InvocationPending {
					t.Fatalf("expired lane mutation=%+v,%v", old, err)
				}
				var brokerState string
				if err := pool.QueryRow(ctx, `SELECT state FROM trigger_records WHERE id=$1`, brokerID).Scan(&brokerState); err != nil || brokerState != "pending" {
					t.Fatalf("expired broker mutation=%s,%v", brokerState, err)
				}
				var sequence, history, cancellations int
				if err := pool.QueryRow(ctx, `SELECT next_sequence,(SELECT count(*) FROM event_fanout_attempt_history),(SELECT count(*) FROM invocation_work_cancellations) FROM invocation_work_lanes WHERE app_id=$1 AND policy_name='orders'`, recipient.AppID).Scan(&sequence, &history, &cancellations); err != nil || sequence != 3 || history != 0 || cancellations != 0 {
					t.Fatalf("partial commit seq=%d history=%d cancellations=%d err=%v", sequence, history, cancellations, err)
				}
			})
		}
	}
}

func TestPgEventRoutingAdmissionRejectsTransferredTarget(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(fmt.Sprintf("independent=%t", adopted), func(t *testing.T) {
			pg, pool, ctx := pgStoreWithPool(t)
			claim, receipt, _ := seedRoutingAdmission(t, pg, adopted, "")
			other, err := pg.CreateAccount(ctx, "new-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE apps SET account_id=$1 WHERE id=$2`, other.ID, receipt.RecipientSnapshot[0].AppID); err != nil {
				t.Fatal(err)
			}
			_, err = pg.AdmitPublishedEventRecipient(ctx, claim)
			var classified *state.EventRecipientAdmissionError
			if !errors.As(err, &classified) || !errors.Is(err, state.ErrNotFound) || classified.Retryable || classified.FailureCode != state.EventFanoutFailureCodeTargetUnavailable {
				t.Fatalf("transferred owner=%v", err)
			}
		})
	}
}

func TestPgEventRoutingAdmissionRejectsLeaseExpiredWhileWaiting(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		for _, action := range []string{"", state.EventWorkInvoke, state.EventWorkCancelPending} {
			t.Run(fmt.Sprintf("independent=%t/action=%s", adopted, action), func(t *testing.T) {
				pg, pool, _ := pgStoreWithPool(t)
				claim, receipt, _ := seedRoutingAdmission(t, pg, adopted, action)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				query := `UPDATE event_fanout_outbox SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE id=$1`
				if adopted {
					query = `UPDATE event_fanout_recipients SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE outbox_id=$1`
				}
				if _, err := pool.Exec(ctx, query, claim.OutboxID); err != nil {
					t.Fatal(err)
				}
				blocker, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = blocker.Rollback(context.Background()) }()
				if _, err := blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, receipt.RecipientSnapshot[0].AppID); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := pg.AdmitPublishedEventRecipient(ctx, claim); done <- err }()
				time.Sleep(300 * time.Millisecond)
				if err := blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-done; !errors.Is(err, state.ErrConflict) {
					t.Fatalf("expired admission=%v", err)
				}
				rows, err := pg.ListInvocationsForApp(ctx, receipt.RecipientSnapshot[0].AppID)
				if err != nil || len(rows) != 0 {
					t.Fatalf("stale admission created rows=%+v,%v", rows, err)
				}
				var n int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_fanout_attempt_history`).Scan(&n); err != nil || n != 0 {
					t.Fatalf("stale history=%d,%v", n, err)
				}
			})
		}
	}
}

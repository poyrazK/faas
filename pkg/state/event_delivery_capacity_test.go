package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

type capacityFixture struct {
	store             routingAdmissionTestStore
	account, app, sub string
	adopted           bool
	source            string
}

func newCapacityFixture(t *testing.T, store routingAdmissionTestStore, adopted bool) capacityFixture {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "capacity-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.MustParse(account.ID).String()
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "capacity-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	return capacityFixture{store: store, account: accountID, app: app.ID, sub: sub.ID, adopted: adopted, source: "orders"}
}
func (f capacityFixture) publish(t *testing.T, key string) (*state.PublishedEventWork, events.Envelope) {
	t.Helper()
	ctx := context.Background()
	data, _ := json.Marshal(map[string]string{"order_id": key})
	envelope, err := (events.Envelope{ID: uuid.NewString(), Source: f.source, Type: "order.created", Data: data}).Normalize(f.account, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(envelope)
	if err := f.store.AppendEvent(ctx, "apid", "event.published", &f.account, payload); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if f.adopted {
		if err := f.store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return receipt, envelope
}
func (f capacityFixture) claim(t *testing.T, receipt *state.PublishedEventWork, subscription string) state.PublishedEventRoutingClaim {
	t.Helper()
	claim := state.PublishedEventRoutingClaim{OutboxID: receipt.ID, SubscriptionID: subscription, ClaimToken: receipt.ClaimToken}
	if f.adopted {
		work, err := f.store.ClaimDuePublishedEventRecipient(context.Background(), time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if work.OutboxID != receipt.ID || work.Recipient.ID != subscription {
			t.Fatalf("claim=%+v want %d/%s", work, receipt.ID, subscription)
		}
		claim.ClaimToken, claim.Generation = work.ClaimToken, work.Generation
	}
	return claim
}
func (f capacityFixture) fill(t *testing.T, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		receipt, _ := f.publish(t, fmt.Sprint(i))
		r, err := f.store.AdmitPublishedEventRecipient(context.Background(), f.claim(t, receipt, f.sub))
		if err != nil || !r.InvocationCreated {
			t.Fatalf("fill[%d]=%+v,%v", i, r, err)
		}
		ids = append(ids, r.DeliveryID)
	}
	return ids
}

func TestEventDeliveryCapacityIsolationAndRecovery(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store, adopted)
		ids := f.fill(t, api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer)
		// Billing joins after analytics filled its live delivery cap.
		billing, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: f.account, Slug: "billing-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		sub, _, err := store.UpsertEventSubscription(ctx, f.account, billing.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		receipt, envelope := f.publish(t, "shared")
		var deferred state.PublishedEventRoutingResult
		// Claim ordering is fair, so route whichever sibling is claimed first.
		for range 2 {
			claim := state.PublishedEventRoutingClaim{OutboxID: receipt.ID, ClaimToken: receipt.ClaimToken}
			if adopted {
				work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				claim.SubscriptionID, claim.ClaimToken, claim.Generation = work.Recipient.ID, work.ClaimToken, work.Generation
			} else {
				if deferred.Progress.State == "" {
					claim.SubscriptionID = f.sub
				} else {
					claim.SubscriptionID = sub.ID
				}
			}
			r, err := store.AdmitPublishedEventRecipient(ctx, claim)
			if err != nil {
				t.Fatal(err)
			}
			if claim.SubscriptionID == f.sub {
				deferred = r
				if !r.CapacityDeferred || r.Progress.CapacityScope != "consumer" || r.InvocationCreated {
					t.Fatalf("analytics=%+v", r)
				}
			} else if !r.InvocationCreated {
				t.Fatalf("billing=%+v", r)
			}
		}
		if _, err := store.InvocationByID(ctx, state.PublishedEventInvocationID(f.account, "orders", envelope.ID, sub.ID)); err != nil {
			t.Fatal(err)
		}
		// More than twelve capacity waits must never dead-letter analytics.
		for i := 1; i < 20; i++ {
			if !adopted {
				progress := deferred.Progress
				at := time.Now().Add(-time.Second)
				progress.NextAttemptAt = &at
				if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, f.sub, progress); err != nil {
					t.Fatal(err)
				}
			}
			r, err := store.AdmitPublishedEventRecipient(ctx, f.claim(t, receipt, f.sub))
			if err != nil || !r.CapacityDeferred {
				t.Fatalf("deferral[%d]=%+v,%v", i, r, err)
			}
			deferred = r
		}
		if deferred.Progress.CapacityDeferrals != 20 || deferred.Progress.Attempts != 20 {
			t.Fatalf("durable budget=%+v", deferred)
		}
		if !adopted {
			stale := deferred.Progress
			stale.CapacityDeferrals--
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, f.sub, stale); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("uncertain acknowledgement lowered durable capacity count: %v", err)
			}
		}
		receiptStore := store.(state.EventReceiptStore)
		read, err := receiptStore.EventReceipt(ctx, f.account, "orders", envelope.ID, state.EventReceiptCursor{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		var routing state.EventReceiptRouting
		for _, entry := range read.Recipients {
			if entry.SubscriptionID == f.sub {
				routing = entry.Routing
			}
		}
		if routing.State != "pending" || routing.CapacityDeferrals != 20 || routing.NextAttemptAt == nil || routing.PendingAgeSeconds == nil || routing.FailureCode != "" {
			t.Fatalf("receipt=%+v", routing)
		}
		if adopted && (routing.GenerationCapacityDeferrals == nil || *routing.GenerationCapacityDeferrals != 20) {
			t.Fatalf("generation deferrals=%+v", routing)
		}
		h, err := store.(state.EventRoutingHealthStore).EventRoutingHealth(ctx)
		if err != nil || h.CapacityWaiting != 1 || h.OldestPendingSeconds <= 0 {
			t.Fatalf("health=%+v,%v", h, err)
		}
		if err := store.CancelInvocation(ctx, ids[0]); err != nil {
			t.Fatal(err)
		}
		if !adopted {
			p := deferred.Progress
			at := time.Now().Add(-time.Second)
			p.NextAttemptAt = &at
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, f.sub, p); err != nil {
				t.Fatal(err)
			}
		}
		claim := f.claim(t, receipt, f.sub)
		r, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || !r.InvocationCreated || !r.ReceiptSettled || r.Progress.CapacityDeferrals != 20 || r.Progress.CapacityScope != "" {
			t.Fatalf("resume=%+v,%v", r, err)
		}
		duplicate, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || duplicate.InvocationCreated {
			t.Fatalf("duplicate=%+v,%v", duplicate, err)
		}
		h, err = store.(state.EventRoutingHealthStore).EventRoutingHealth(ctx)
		if err != nil || h.CapacityWaiting != 0 || h.OldestPendingSeconds != 0 {
			t.Fatalf("settled health=%+v,%v", h, err)
		}
	})
}

func TestPgEventDeliveryCapacityConcurrentAdmission(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(fmt.Sprint(adopted), func(t *testing.T) {
			pg, _, ctx := pgStoreWithPool(t)
			f := newCapacityFixture(t, pg, adopted)
			f.fill(t, api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer-1)
			var claims []state.PublishedEventRoutingClaim
			for range 16 {
				receipt, _ := f.publish(t, "race")
				claims = append(claims, f.claim(t, receipt, f.sub))
			}
			var wg sync.WaitGroup
			results := make(chan state.PublishedEventRoutingResult, 16)
			errs := make(chan error, 16)
			for _, claim := range claims {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := pg.AdmitPublishedEventRecipient(ctx, claim)
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
			created, deferred := 0, 0
			for r := range results {
				if r.InvocationCreated {
					created++
				}
				if r.CapacityDeferred {
					deferred++
				}
			}
			if created != 1 || deferred != 15 {
				t.Fatalf("created=%d deferred=%d", created, deferred)
			}
		})
	}
}

func TestEventDeliveryReplaySharesConsumerCapacity(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store, adopted)
		ids := f.fill(t, api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer)
		if err := store.FailInvocation(ctx, ids[0], "handler failed", 0, 0); err != nil {
			t.Fatal(err)
		}
		f.fill(t, 1)
		replayStore := store.(state.PlainInvocationReplayStore)
		if _, err := replayStore.ReplayPlainInvocation(ctx, f.account, ids[0], state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrEventDeliveryCapacity) {
			t.Fatalf("capacity bypass=%v", err)
		}
		if _, err := replayStore.ExistingPlainInvocationReplay(ctx, f.account, ids[0]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rejected replay left identity=%v", err)
		}
		if err := store.CancelInvocation(ctx, ids[1]); err != nil {
			t.Fatal(err)
		}
		replay, err := replayStore.ReplayPlainInvocation(ctx, f.account, ids[0], state.PlainInvocationReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		again, err := replayStore.ReplayPlainInvocation(ctx, f.account, ids[0], state.PlainInvocationReplayOptions{})
		if err != nil || again.ID != replay.ID {
			t.Fatalf("full duplicate replay=%+v,%v", again, err)
		}
		// A descendant retains its slot identity even after the root is pruned.
		if err := store.FailInvocation(ctx, replay.ID, "failed again", 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DeleteInvocationsByIDs(ctx, []string{ids[0]}); err != nil {
			t.Fatal(err)
		}
		f.fill(t, 1)
		if _, err := replayStore.ReplayPlainInvocation(ctx, f.account, replay.ID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrEventDeliveryCapacity) {
			t.Fatalf("pruned ancestor capacity bypass=%v", err)
		}
	})
}

func TestEventRoutingFairClaimsAcrossAccountsAndConsumers(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		a := newCapacityFixture(t, store.(routingAdmissionTestStore), false)
		b := newCapacityFixture(t, store.(routingAdmissionTestStore), false)
		// Queue three older receipts for one account and one for a second account.
		for _, f := range []capacityFixture{a, a, a, b} {
			envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(f.account, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(envelope)
			if err := store.AppendEvent(ctx, "apid", "event.published", &f.account, payload); err != nil {
				t.Fatal(err)
			}
		}
		first, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if first.RecipientSnapshot[0].AccountID != a.account || second.RecipientSnapshot[0].AccountID != b.account {
			t.Fatalf("account fairness first=%+v second=%+v", first, second)
		}
		// Adopt the receipts and ensure the same account cannot monopolize claims.
		if err := store.InitializePublishedEventRecipients(ctx, first, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, second, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		one, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		two, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if one.Recipient.AccountID == two.Recipient.AccountID {
			t.Fatalf("recipient account fairness=%+v/%+v", one, two)
		}
	})
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store.(routingAdmissionTestStore), true)
		if _, _, err := store.UpsertEventSubscription(ctx, f.account, f.app, "orders", "*", nil); err != nil {
			t.Fatal(err)
		}
		receipt, _ := f.publish(t, "two-consumers")
		// Both consumers are present in two accepted events; the first consumer's
		// older second event must not outrank the other consumer's first event.
		envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(f.account, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(envelope)
		if err := store.AppendEvent(ctx, "apid", "event.published", &f.account, payload); err != nil {
			t.Fatal(err)
		}
		other, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, other, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		one, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		two, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.RecipientSnapshot) != 2 || one.Recipient.ID == two.Recipient.ID {
			t.Fatalf("consumer fairness=%+v/%+v", one, two)
		}
	})
}

func TestEventDeliveryInPlaceReplaySharesCapacity(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store, adopted)
		ids := f.fill(t, api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer)
		if _, err := store.ClaimInvocation(ctx, ids[0], "", 30); err != nil {
			t.Fatal(err)
		}
		if err := store.FailInvocation(ctx, ids[0], "retry exhausted", time.Second, 1); err != nil {
			t.Fatal(err)
		}
		f.fill(t, 1)
		if _, err := store.RetryQueueDeadLetter(ctx, f.account, ids[0]); !errors.Is(err, state.ErrEventDeliveryCapacity) {
			t.Fatalf("in-place bypass=%v", err)
		}
		original, err := store.InvocationByID(ctx, ids[0])
		if err != nil || original.State != state.InvocationDeadLetter || original.ReplayGeneration != 0 {
			t.Fatalf("rejection mutated original=%+v,%v", original, err)
		}
		if err := store.CancelInvocation(ctx, ids[1]); err != nil {
			t.Fatal(err)
		}
		replay, err := store.RetryQueueDeadLetter(ctx, f.account, ids[0])
		if err != nil || replay.State != state.InvocationPending || replay.ReplayGeneration != 1 {
			t.Fatalf("in-place resume=%+v,%v", replay, err)
		}
	})
}

func TestEventDeliveryKeepLatestAndKeyedReplayAtCapacity(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store, adopted)
		policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
		if _, err := store.UpsertAppWorkPolicy(ctx, f.account, f.app, policy); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventWorkBinding(ctx, f.app, f.sub, policy.Name, "data.order_id", state.EventWorkBindingOptions{Action: state.EventWorkInvoke}); err != nil {
			t.Fatal(err)
		}
		cap := api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer
		ids := f.fill(t, cap)
		receipt, _ := f.publish(t, fmt.Sprint(cap-1))
		r, err := store.AdmitPublishedEventRecipient(ctx, f.claim(t, receipt, f.sub))
		if err != nil || !r.InvocationCreated || r.CapacityDeferred {
			t.Fatalf("net-zero replacement=%+v,%v", r, err)
		}
		old, err := store.InvocationByID(ctx, ids[cap-1])
		if err != nil || old.State != state.InvocationSuperseded {
			t.Fatalf("replacement state=%+v,%v", old, err)
		}
		if err := store.FailInvocation(ctx, ids[0], "failed", 0, 0); err != nil {
			t.Fatal(err)
		}
		receipt, _ = f.publish(t, "new-key")
		if r, err := store.AdmitPublishedEventRecipient(ctx, f.claim(t, receipt, f.sub)); err != nil || !r.InvocationCreated {
			t.Fatalf("refill=%+v,%v", r, err)
		}
		replayStore := store.(state.KeyedInvocationReplayStore)
		if _, err := replayStore.ReplayKeyedInvocation(ctx, f.account, ids[0], state.KeyedInvocationReplayOptions{}); !errors.Is(err, state.ErrEventDeliveryCapacity) {
			t.Fatalf("keyed replay bypass=%v", err)
		}
		if _, err := replayStore.ExistingKeyedInvocationReplay(ctx, f.account, ids[0]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("keyed rejection marker=%v", err)
		}
		// Cancellation remains available at capacity and frees its pending slot.
		if _, err := store.SetEventWorkBinding(ctx, f.app, f.sub, policy.Name, "data.order_id", state.EventWorkBindingOptions{Action: state.EventWorkCancelPending}); err != nil {
			t.Fatal(err)
		}
		receipt, _ = f.publish(t, "1")
		r, err = store.AdmitPublishedEventRecipient(ctx, f.claim(t, receipt, f.sub))
		if err != nil || r.CapacityDeferred || r.Progress.State != state.PublishedEventRecipientEnqueued {
			t.Fatalf("cancel at capacity=%+v,%v", r, err)
		}
		if _, err := replayStore.ReplayKeyedInvocation(ctx, f.account, ids[0], state.KeyedInvocationReplayOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEventWholeRoutingFairConsumerBehindBacklog(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		f := newCapacityFixture(t, store.(routingAdmissionTestStore), false)
		f.publish(t, "already-served")
		appendEvent := func(kind string) {
			t.Helper()
			e, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: kind, Data: json.RawMessage(`{}`)}).Normalize(f.account, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(e)
			if err := store.AppendEvent(ctx, "apid", "event.published", &f.account, payload); err != nil {
				t.Fatal(err)
			}
		}
		for range 20 {
			appendEvent("order.created")
		}
		billing, _, err := store.UpsertEventSubscription(ctx, f.account, f.app, "orders", "billing.ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		appendEvent("billing.ready")
		// Billing must outrank older receipts for the recently visited consumer,
		// even while independent-recipient adoption remains disabled.
		chosen, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(chosen.RecipientSnapshot) != 1 || chosen.RecipientSnapshot[0].ID != billing.ID {
			t.Fatalf("healthy consumer starved behind backlog: %+v", chosen)
		}
	})
}

func TestEventDeliveryApplicationAndAccountCapacityAndPlanChange(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, base recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		store := base.(routingAdmissionTestStore)
		f := newCapacityFixture(t, store, false)
		limits := api.MustLimitsFor(api.PlanFree).EventDeliveries
		consumer := func(app, source string) capacityFixture {
			t.Helper()
			sub, _, err := store.UpsertEventSubscription(ctx, f.account, app, source, "order.created", nil)
			if err != nil {
				t.Fatal(err)
			}
			return capacityFixture{store: store, account: f.account, app: app, sub: sub.ID, source: source}
		}
		for i := 0; i < limits.PerApp/limits.PerConsumer; i++ {
			consumer(f.app, fmt.Sprintf("first-app.%d", i)).fill(t, limits.PerConsumer)
		}
		fullApp := consumer(f.app, "full-app")
		receipt, _ := fullApp.publish(t, "blocked-app")
		r, err := store.AdmitPublishedEventRecipient(ctx, fullApp.claim(t, receipt, fullApp.sub))
		if err != nil || r.Progress.CapacityScope != "app" {
			t.Fatalf("app capacity=%+v,%v", r, err)
		}
		for i := 1; i < limits.PerAccount/limits.PerApp; i++ {
			app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: f.account, Slug: "aggregate-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			for j := 0; j < limits.PerApp/limits.PerConsumer; j++ {
				consumer(app.ID, fmt.Sprintf("other-app.%d.%d", i, j)).fill(t, limits.PerConsumer)
			}
		}
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: f.account, Slug: "account-cap-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		fullAccount := consumer(app.ID, "full-account")
		receipt, _ = fullAccount.publish(t, "blocked-account")
		claim := fullAccount.claim(t, receipt, fullAccount.sub)
		r, err = store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || r.Progress.CapacityScope != "account" {
			t.Fatalf("account capacity=%+v,%v", r, err)
		}
		account, err := store.AccountByID(ctx, f.account)
		if errors.Is(err, state.ErrNotFound) {
			account, err = store.AccountByID(ctx, strings.ReplaceAll(f.account, "-", ""))
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		r, err = store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || !r.InvocationCreated || r.CapacityDeferred {
			t.Fatalf("current plan capacity=%+v,%v", r, err)
		}
	})
}

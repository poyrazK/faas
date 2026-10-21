package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// adr: 614 — a batch claim takes the recipients back-to-back single claims
// would take, in their order: one per eligible account per round, rotating
// each account's consumers, with service times carried into the next batch.
func TestEventRecipientBatchClaimMatchesSingleClaimOrder(t *testing.T) {
	forRecipientBatchStores(t, func(t *testing.T, newStore func() recipientClaimTestStore) {
		single := claimOrderScenario(t, newStore(), func(store recipientClaimTestStore, now time.Time) []*state.PublishedEventRecipientWork {
			var works []*state.PublishedEventRecipientWork
			for {
				work, err := store.ClaimDuePublishedEventRecipient(context.Background(), now)
				if errors.Is(err, state.ErrNotFound) {
					return works
				}
				if err != nil {
					t.Fatal(err)
				}
				works = append(works, work)
			}
		})
		batched := claimOrderScenario(t, newStore(), func(store recipientClaimTestStore, now time.Time) []*state.PublishedEventRecipientWork {
			var works []*state.PublishedEventRecipientWork
			for _, limit := range []int{4, 10, 10} {
				batch, err := store.ClaimDuePublishedEventRecipients(context.Background(), now, limit, true)
				if err != nil {
					t.Fatal(err)
				}
				works = append(works, batch...)
			}
			return works
		})
		if !slices.Equal(single, batched) {
			t.Fatalf("batch order %v, single-claim order %v", batched, single)
		}
		if len(single) != 6 {
			t.Fatalf("claimed %v, want all six deliveries", single)
		}
		// Both accounts hold three deliveries, so service alternates them.
		for i := 1; i < len(single); i++ {
			if single[i][:1] == single[i-1][:1] {
				t.Fatalf("claims %v do not alternate accounts", single)
			}
		}
	})
}

// claimOrderScenario publishes six events for consumers a1, a2 (one account)
// and b1 (another), adopts them all, claims them with claim and returns the
// consumer/event labels in claim order.
func claimOrderScenario(t *testing.T, store recipientClaimTestStore, claim func(recipientClaimTestStore, time.Time) []*state.PublishedEventRecipientWork) []string {
	t.Helper()
	ctx := context.Background()
	consumers := map[string]string{}
	accounts := map[string]string{}
	for _, name := range []string{"a", "b"} {
		account, err := store.CreateAccount(ctx, "batch-order-"+name+"-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accounts[name] = account.ID
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "batch-order-" + name,
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		for _, consumer := range []string{name + "1", name + "2"} {
			if name == "b" && consumer == "b2" {
				continue
			}
			subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", consumer, nil)
			if err != nil {
				t.Fatal(err)
			}
			consumers[subscription.ID] = consumer
		}
	}
	for i, consumer := range []string{"a1", "a1", "a2", "b1", "b1", "b1"} {
		publishRecipientEvent(t, store, accounts[consumer[:1]], consumer, "e"+string(rune('0'+i)), `{"n":1}`)
	}
	now := time.Now().UTC()
	adoptAllReceipts(t, store, now)
	var labels []string
	for _, work := range claim(store, now) {
		labels = append(labels, consumers[work.Recipient.ID]+"/"+recipientEventID(t, work))
	}
	return labels
}

// adr: 614 — a consumer with delivery controls contributes one recipient per
// batch, so admission re-reads its counters before the next claim; a
// consumer without controls is claimed as deep as the batch allows.
func TestEventRecipientBatchClaimHoldsControlledConsumerToOne(t *testing.T) {
	forRecipientBatchStores(t, func(t *testing.T, newStore func() recipientClaimTestStore) {
		store := newStore()
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "batch-controls-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "batch-controls",
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		controlled, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "controlled", nil)
		if err != nil {
			t.Fatal(err)
		}
		plain, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventSubscriptionDeliveryControl(ctx, account.ID, app.ID, controlled.ID, false, api.EventSubscriptionDrainRateMax); err != nil {
			t.Fatal(err)
		}
		for i := range 3 {
			publishRecipientEvent(t, store, account.ID, "controlled", "controlled-"+string(rune('0'+i)), `{"n":1}`)
			publishRecipientEvent(t, store, account.ID, "plain", "plain-"+string(rune('0'+i)), `{"n":1}`)
		}
		now := time.Now().UTC()
		adoptAllReceipts(t, store, now)
		batch, err := store.ClaimDuePublishedEventRecipients(ctx, now, 10, true)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, work := range batch {
			counts[work.Recipient.ID]++
		}
		if counts[controlled.ID] != 1 || counts[plain.ID] != 3 {
			t.Fatalf("batch per consumer = %v, want controlled 1 and plain 3", counts)
		}
		next, err := store.ClaimDuePublishedEventRecipients(ctx, now, 10, true)
		if err != nil || len(next) != 1 || next[0].Recipient.ID != controlled.ID {
			t.Fatalf("next batch = %+v, %v; want the controlled consumer's next delivery", next, err)
		}
	})
}

// adr: 795 — a batch never claims a younger delivery of an ordered lane while
// an earlier one in the same lane is unsettled; other keys proceed.
func TestEventRecipientBatchClaimKeepsOrderedLanes(t *testing.T) {
	forRecipientBatchStores(t, func(t *testing.T, newStore func() recipientClaimTestStore) {
		store := newStore()
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "batch-ordered-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "batch-ordered",
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		policy := workpolicy.Policy{Name: "event-order", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
		if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
			t.Fatal(err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.changed", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventWorkBinding(ctx, app.ID, subscription.ID, policy.Name, "data.order_id",
			state.EventWorkBindingOptions{Ordered: true}); err != nil {
			t.Fatal(err)
		}
		publishRecipientEvent(t, store, account.ID, "order.changed", "ordered-1", `{"order_id":"order-a"}`)
		publishRecipientEvent(t, store, account.ID, "order.changed", "ordered-2", `{"order_id":"order-a"}`)
		publishRecipientEvent(t, store, account.ID, "order.changed", "ordered-other-key", `{"order_id":"order-b"}`)
		now := time.Now().UTC()
		adoptAllReceipts(t, store, now)
		batch, err := store.ClaimDuePublishedEventRecipients(ctx, now, 10, true)
		if err != nil {
			t.Fatal(err)
		}
		var claimed []string
		for _, work := range batch {
			claimed = append(claimed, recipientEventID(t, work))
		}
		slices.Sort(claimed)
		if !slices.Equal(claimed, []string{"ordered-1", "ordered-other-key"}) {
			t.Fatalf("batch claimed %v; the younger same-key delivery must wait", claimed)
		}
	})
}

// adr: 614 — a released claim returns as if never claimed: due again with its
// attempt unspent, and its old token can neither finish nor release it.
func TestEventRecipientBatchClaimRelease(t *testing.T) {
	forRecipientBatchStores(t, func(t *testing.T, newStore func() recipientClaimTestStore) {
		store := newStore()
		ctx, _, _, receipt := seedRecipientClaims(t, store)
		now := time.Now().UTC()
		if err := store.InitializePublishedEventRecipients(ctx, receipt, now); err != nil {
			t.Fatal(err)
		}
		batch, err := store.ClaimDuePublishedEventRecipients(ctx, now, 2, true)
		if err != nil || len(batch) != 2 {
			t.Fatalf("batch = %+v, %v", batch, err)
		}
		released := batch[1]
		if err := store.ReleasePublishedEventRecipientClaim(ctx, released); err != nil {
			t.Fatal(err)
		}
		if err := store.ReleasePublishedEventRecipientClaim(ctx, released); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second release = %v, want ErrConflict", err)
		}
		stale := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: released.TotalAttempts, UpdatedAt: now}
		if err := store.FinishPublishedEventRecipient(ctx, released, stale, now); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("finish after release = %v, want ErrConflict", err)
		}
		again, err := store.ClaimDuePublishedEventRecipients(ctx, now, 10, true)
		if err != nil {
			t.Fatal(err)
		}
		var reclaimed *state.PublishedEventRecipientWork
		for _, work := range again {
			if work.Recipient.ID == released.Recipient.ID {
				reclaimed = work
			}
		}
		if reclaimed == nil || reclaimed.Attempts != 1 || reclaimed.TotalAttempts != 1 {
			t.Fatalf("reclaimed = %+v; want the released delivery on its first attempt", reclaimed)
		}
		finishRecipient(t, store, batch[0], state.PublishedEventRecipientEnqueued)
		for _, work := range again {
			finishRecipient(t, store, work, state.PublishedEventRecipientEnqueued)
		}
	})
}

func forRecipientBatchStores(t *testing.T, test func(*testing.T, func() recipientClaimTestStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		test(t, func() recipientClaimTestStore { return state.NewMemStore() })
	})
	t.Run("postgres", func(t *testing.T) {
		test(t, func() recipientClaimTestStore {
			store, _, _ := pgStoreWithPool(t)
			return store
		})
	})
}

func publishRecipientEvent(t *testing.T, store recipientClaimTestStore, accountID, eventType, id, data string) {
	t.Helper()
	envelope, err := (events.Envelope{ID: id, Source: "orders", Type: eventType, Data: json.RawMessage(data)}).Normalize(accountID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
}

// adoptAllReceipts adopts every published receipt for independent recipient
// routing, as the fanout sweep does before it claims recipients.
func adoptAllReceipts(t *testing.T, store recipientClaimTestStore, now time.Time) {
	t.Helper()
	ctx := context.Background()
	for {
		receipt, err := store.ClaimDuePublishedEventForRecipientAdoption(ctx, now)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, receipt, now); err != nil {
			t.Fatal(err)
		}
	}
}

func recipientEventID(t *testing.T, work *state.PublishedEventRecipientWork) string {
	t.Helper()
	var envelope events.Envelope
	if err := json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.ID
}

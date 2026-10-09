package state_test

import (
	"context"
	"encoding/json"
	"errors"
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

type recipientClaimTestStore interface {
	state.Store
	state.AppWorkPolicyStore
	state.EventWorkBindingStore
	state.EventSubscriptionStore
	state.PublishedEventWorkStore
	state.PublishedEventRecipientWorkStore
	state.PublishedEventRecipientProgressStore
	state.EventFanoutReplayStore
	state.EventFanoutReplayBatchStore
	state.EventFanoutAttemptHistoryStore
	state.PublishedEventRetentionStore
	state.EventReceiptStore
	state.EventReceiptReplayStore
	state.EventReceiptAcceptanceStore
	state.WorkCancellationStore
}

func TestEventRecipientOrderedKeyBlocksYoungerUntilRetrySettles(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "ordered-events-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID,
			Slug: "ordered-events", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
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
		append := func(id, key string) {
			t.Helper()
			envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.changed",
				Data: json.RawMessage(`{"order_id":"` + key + `"}`)}).Normalize(account.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
				t.Fatal(err)
			}
		}
		append("ordered-1", "order-a")
		append("ordered-2", "order-a")
		append("ordered-other-key", "order-b")

		now := time.Now().UTC()
		first, err := store.ClaimDuePublishedEvent(ctx, now)
		if err != nil || receiptEventID(t, first) != "ordered-1" || first.RecipientSnapshot[0].Work == nil || !first.RecipientSnapshot[0].Work.Ordered {
			t.Fatalf("first ordered receipt = %+v, %v", first, err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, first, now); err != nil {
			t.Fatal(err)
		}
		otherKey, err := store.ClaimDuePublishedEvent(ctx, now)
		if err != nil || receiptEventID(t, otherKey) != "ordered-other-key" {
			t.Fatalf("independent key receipt = %+v, %v", otherKey, err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, otherKey, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimDuePublishedEvent(ctx, now); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("younger same-key receipt routed early: %v", err)
		}

		firstDelivery, err := store.ClaimDuePublishedEventRecipient(ctx, now)
		if err != nil || firstDelivery.OutboxID != first.ID {
			t.Fatalf("first same-key delivery = %+v, %v", firstDelivery, err)
		}
		otherDelivery, err := store.ClaimDuePublishedEventRecipient(ctx, now)
		if err != nil || otherDelivery.OutboxID != otherKey.ID {
			t.Fatalf("different-key delivery did not proceed independently: %+v, %v", otherDelivery, err)
		}
		if _, err := store.ClaimDuePublishedEventRecipient(ctx, now); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("younger same-key recipient claimed while earlier delivery active: %v", err)
		}
		finishRecipient(t, store, firstDelivery, state.PublishedEventRecipientPending)
		finishRecipient(t, store, otherDelivery, state.PublishedEventRecipientEnqueued)

		retryAt := now.Add(2 * time.Minute)
		if _, err := store.ClaimDuePublishedEvent(ctx, retryAt); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("younger receipt routed while earlier delivery awaited retry: %v", err)
		}
		retry, err := store.ClaimDuePublishedEventRecipient(ctx, retryAt)
		if err != nil || retry.OutboxID != first.ID {
			t.Fatalf("earlier delivery retry = %+v, %v", retry, err)
		}
		finishRecipient(t, store, retry, state.PublishedEventRecipientEnqueued)

		younger, err := store.ClaimDuePublishedEvent(ctx, retryAt)
		if err != nil || receiptEventID(t, younger) != "ordered-2" {
			t.Fatalf("younger receipt after prior admission = %+v, %v", younger, err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, younger, retryAt); err != nil {
			t.Fatal(err)
		}
		youngerDelivery, err := store.ClaimDuePublishedEventRecipient(ctx, retryAt)
		if err != nil || youngerDelivery.OutboxID != younger.ID {
			t.Fatalf("younger same-key delivery after retry = %+v, %v", youngerDelivery, err)
		}
		finishRecipient(t, store, youngerDelivery, state.PublishedEventRecipientEnqueued)
	})
}

func receiptEventID(t *testing.T, receipt *state.PublishedEventWork) string {
	t.Helper()
	if receipt == nil {
		t.Fatal("event receipt is nil")
	}
	var envelope events.Envelope
	if err := json.Unmarshal(receipt.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.ID
}

// ADR-606: adopting a replayed legacy receipt starts a new routing budget;
// exhausted lifetime counters must not immediately exhaust that generation.
func TestEventRecipientClaimsAdoptReplayedLegacyBudget(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, accountID, appID, receipt := seedRecipientClaims(t, store)
		for i, recipient := range receipt.RecipientSnapshot {
			progress := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: 1, UpdatedAt: time.Now().UTC()}
			if i == 0 {
				progress.State = state.PublishedEventRecipientFailed
				progress.Attempts = 12
				progress.FailureCode = state.EventFanoutFailureCodeInvocationEnqueueFailed
				progress.Retryable = true
				progress.LastError = "legacy budget exhausted"
			}
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, recipient.ID, progress); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.FinishPublishedEvent(ctx, receipt.ID, receipt.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		failedID := receipt.RecipientSnapshot[0].ID
		if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, appID, "orders", "evt-three-consumers", failedID); err != nil {
			t.Fatal(err)
		}
		replayed, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, replayed, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil || work.Recipient.ID != failedID || work.Attempts != 1 || work.TotalAttempts != 13 {
			t.Fatalf("legacy replay adoption = %+v, %v", work, err)
		}
		finishRecipient(t, store, work, state.PublishedEventRecipientEnqueued)
		if _, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("adoption rerouted successful legacy sibling: %v", err)
		}
	})
}

func forRecipientClaimStores(t *testing.T, test func(*testing.T, recipientClaimTestStore, *pgxpool.Pool)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore(), nil) })
	t.Run("postgres", func(t *testing.T) {
		store, pool, _ := pgStoreWithPool(t)
		test(t, store, pool)
	})
}

func seedRecipientClaims(t *testing.T, store recipientClaimTestStore) (context.Context, string, string, *state.PublishedEventWork) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "recipient-claims@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		ID: uuid.NewString(), AccountID: id.String(), Slug: "recipient-claims", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range [][2]string{{"orders", "order.created"}, {"orders", "*"}, {"*", "order.created"}} {
		if _, _, err := store.UpsertEventSubscription(ctx, id.String(), app.ID, pattern[0], pattern[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	envelope, err := (events.Envelope{ID: "evt-three-consumers", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(id.String(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	accountID := id.String()
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return ctx, accountID, app.ID, work
}

func finishRecipient(t *testing.T, store recipientClaimTestStore, work *state.PublishedEventRecipientWork, status string) {
	t.Helper()
	now := time.Now().UTC()
	progress := state.PublishedEventRecipientProgress{State: status, Attempts: work.TotalAttempts, UpdatedAt: now}
	if status == state.PublishedEventRecipientFailed {
		progress.FailureCode = state.EventFanoutFailureCodeInvocationEnqueueFailed
		progress.Retryable = true
		progress.LastError = "recipient outage"
	}
	if err := store.FinishPublishedEventRecipient(context.Background(), work, progress, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

// ADR-606: independent recipient leases and replay generations preserve siblings.
func TestEventRecipientClaimsRecoverIndependently(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, accountID, appID, receipt := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second adoption = %v", err)
		}
		// Three concurrent workers must obtain three distinct consumer leases.
		claims := make(chan *state.PublishedEventRecipientWork, 3)
		errs := make(chan error, 3)
		var workers sync.WaitGroup
		for range 3 {
			workers.Go(func() {
				work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
				claims <- work
				errs <- err
			})
		}
		workers.Wait()
		seen := map[string]bool{}
		leased := make([]*state.PublishedEventRecipientWork, 0, 3)
		for range 3 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			work := <-claims
			if seen[work.Recipient.ID] {
				t.Fatal("two workers claimed the same consumer")
			}
			seen[work.Recipient.ID] = true
			leased = append(leased, work)
		}
		finishRecipient(t, store, leased[0], state.PublishedEventRecipientEnqueued)
		finishRecipient(t, store, leased[1], state.PublishedEventRecipientFailed)
		// The third consumer is still actively routing. Both single and batch
		// replay must recover the failed consumer without disturbing that lease.
		if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, appID, "orders", "evt-three-consumers", leased[1].Recipient.ID); err != nil {
			t.Fatalf("replay while sibling leased: %v", err)
		}
		replay, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if replay.Recipient.ID != leased[1].Recipient.ID || replay.Generation != 2 || replay.Attempts != 1 || replay.TotalAttempts != 2 {
			t.Fatalf("replay lease = %+v", replay)
		}
		stale := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: leased[1].TotalAttempts, UpdatedAt: time.Now().UTC()}
		if err := store.FinishPublishedEventRecipient(ctx, leased[1], stale, time.Now()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("prior generation completion = %v", err)
		}
		finishRecipient(t, store, replay, state.PublishedEventRecipientFailed)
		batch, err := store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "orders", "evt-three-consumers", 1)
		if err != nil || batch.Replayed != 1 || batch.HasMore {
			t.Fatalf("batch replay while sibling leased = %+v, %v", batch, err)
		}
		replay, err = store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil || replay.Recipient.ID != leased[1].Recipient.ID || replay.Generation != 3 || replay.TotalAttempts != 3 {
			t.Fatalf("second replay lease = %+v, %v", replay, err)
		}
		finishRecipient(t, store, replay, state.PublishedEventRecipientEnqueued)
		// This completion proves the sibling's original token survived both replays.
		finishRecipient(t, store, leased[2], state.PublishedEventRecipientEnqueued)
		if _, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("successful consumers rerouted: %v", err)
		}
		history, err := store.ListEventFanoutAttemptsForApp(ctx, appID, 20, state.EventFanoutAttemptCursor{}, "orders", "evt-three-consumers", leased[1].Recipient.ID)
		if err != nil || len(history) != 5 || history[0].Attempts != 3 || history[1].LastError != "recipient outage" {
			t.Fatalf("attempt/replay lineage = %+v, %v", history, err)
		}
	})
}

// ADR-606: account/app scope survives normalization, concurrent operators
// cannot replay a consumer twice, and receipt settlement survives store restart.
func TestEventRecipientClaimsConcurrentReplayIsScoped(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, pool *pgxpool.Pool) {
		ctx, accountID, appID, receipt := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if pool != nil {
			store = state.NewPgStore(pool)
		}
		leases := make([]*state.PublishedEventRecipientWork, 3)
		for i := range leases {
			var err error
			leases[i], err = store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
		}
		finishRecipient(t, store, leases[0], state.PublishedEventRecipientFailed)
		for _, scope := range [][2]string{{uuid.NewString(), appID}, {accountID, uuid.NewString()}} {
			if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, scope[0], scope[1], "orders", "evt-three-consumers", leases[0].Recipient.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign replay = %v", err)
			}
			batch, err := store.ReplayRetryablePublishedEventRecipientsForApp(ctx, scope[0], scope[1], "orders", "evt-three-consumers", 10)
			if err != nil || batch.Replayed != 0 || batch.HasMore {
				t.Fatalf("foreign batch = %+v, %v", batch, err)
			}
		}
		var workers sync.WaitGroup
		results := make(chan state.EventFanoutReplayBatch, 2)
		errs := make(chan error, 2)
		for range 2 {
			workers.Go(func() {
				batch, err := store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "orders", "evt-three-consumers", 1)
				results <- batch
				errs <- err
			})
		}
		workers.Wait()
		var replayed int
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			replayed += (<-results).Replayed
		}
		if replayed != 1 {
			t.Fatalf("concurrent operators replayed %d recipients", replayed)
		}
		var err error
		leases[0], err = store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil || leases[0].Generation != 2 {
			t.Fatalf("replay after restart = %+v, %v", leases[0], err)
		}
		// Commit all three checkpoints concurrently to exercise parent aggregation.
		for _, lease := range leases {
			workers.Go(func() {
				now := time.Now().UTC()
				errs <- store.FinishPublishedEventRecipient(ctx, lease, state.PublishedEventRecipientProgress{
					State: state.PublishedEventRecipientEnqueued, Attempts: lease.TotalAttempts, UpdatedAt: now,
				}, now)
			})
		}
		// Drain completions before waiting so the bounded error channel cannot block.
		for range leases {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		workers.Wait()
		if n, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(state.PublishedEventIdentityRetention+time.Minute), 100); err != nil || n != 1 {
			t.Fatalf("concurrent settlement = %d, %v", n, err)
		}
	})
}

// ADR-606: expired claims are fenced and retention excludes unfinished routing.
func TestEventRecipientClaimsFenceExpiredWorkersAndRetainActiveWork(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, pool *pgxpool.Pool) {
		ctx, _, _, receipt := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if pool != nil {
			if _, err := pool.Exec(ctx, "UPDATE event_fanout_recipients SET lease_until = clock_timestamp() - interval '1 second' WHERE outbox_id = $1 AND subscription_id = $2", claim.OutboxID, claim.Recipient.ID); err != nil {
				t.Fatal(err)
			}
		}
		expiredAt := claim.LeaseUntil.Add(time.Second)
		progress := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: claim.TotalAttempts, UpdatedAt: expiredAt}
		if err := store.FinishPublishedEventRecipient(ctx, claim, progress, expiredAt); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired worker acknowledged before reclaim: %v", err)
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, expiredAt.Add(state.PublishedEventIdentityRetention), 100); err != nil || n != 0 {
			t.Fatalf("active receipt pruned: %d, %v", n, err)
		}
		var replacement *state.PublishedEventRecipientWork
		for range 3 {
			work, err := store.ClaimDuePublishedEventRecipient(ctx, expiredAt)
			if err != nil {
				t.Fatal(err)
			}
			if work.Recipient.ID == claim.Recipient.ID {
				replacement = work
			}
			finishRecipient(t, store, work, state.PublishedEventRecipientEnqueued)
		}
		if replacement == nil || replacement.ClaimToken == claim.ClaimToken || replacement.TotalAttempts != 2 {
			t.Fatalf("replacement lease = %+v", replacement)
		}
		if err := store.FinishPublishedEventRecipient(ctx, claim, progress, expiredAt); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old worker acknowledged after reclaim: %v", err)
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, expiredAt.Add(state.PublishedEventIdentityRetention), 100); err != nil || n != 1 {
			t.Fatalf("settled receipt retention = %d, %v", n, err)
		}
		if _, err := store.ClaimDuePublishedEventRecipient(ctx, expiredAt.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("orphan recipient remained after retention: %v", err)
		}
	})
}

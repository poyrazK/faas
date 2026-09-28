package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreReplayFailedPublishedEventRecipientKeepsSiblingProgress(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "fanout-replay@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-replay"})
	if err != nil {
		t.Fatal(err)
	}
	failedRecipient, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	successfulRecipient, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-retry","source":"orders","type":"order.created","data":{"amount":1}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, failedRecipient.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 3,
			FailureCode: EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true,
			LastError: "temporary outage", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, successfulRecipient.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientEnqueued, Attempts: 1, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}

	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, app.ID, "orders", "evt-retry", failedRecipient.ID); err != nil {
		t.Fatalf("replay failed recipient: %v", err)
	}
	replayed, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim replay: %v", err)
	}
	if got := replayed.RecipientProgress[failedRecipient.ID]; got.State != PublishedEventRecipientPending || got.Attempts != 3 ||
		got.FailureCode != "" || got.Retryable || got.LastError != "" {
		t.Fatalf("replayed recipient progress = %+v, want pending with prior attempts and cleared failure details", got)
	}
	if got := replayed.RecipientProgress[successfulRecipient.ID]; got.State != PublishedEventRecipientEnqueued || got.Attempts != 1 {
		t.Fatalf("successful sibling progress = %+v, want unchanged enqueued outcome", got)
	}
	if len(replayed.RecipientSnapshot) != 2 || replayed.RecipientSnapshot[0].ID != work.RecipientSnapshot[0].ID ||
		replayed.RecipientSnapshot[1].ID != work.RecipientSnapshot[1].ID {
		t.Fatalf("recipient snapshot changed during replay: before=%+v after=%+v", work.RecipientSnapshot, replayed.RecipientSnapshot)
	}
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, app.ID, "orders", "evt-retry", failedRecipient.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second replay while recipient is pending = %v, want ErrNotFound", err)
	}
	if err := store.RecordPublishedEventRecipientProgress(ctx, replayed.ID, replayed.ClaimToken, failedRecipient.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientEnqueued, Attempts: 4, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishedEvent(ctx, replayed.ID, replayed.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	failures, err := store.ListEventFanoutFailuresForApp(ctx, app.ID, 10, EventFanoutFailureCursor{}, "evt-retry")
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures after successful replay = %+v, want none", failures)
	}
}

func TestMemStoreListEventFanoutFailuresIncludesClassification(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "fanout-classification@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-classification"})
	if err != nil {
		t.Fatal(err)
	}
	recipient, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-classification","source":"orders","type":"order.created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 12,
			FailureCode: EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true,
			LastError: "temporary outage", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}

	failures, err := store.ListEventFanoutFailuresForApp(ctx, app.ID, 10, EventFanoutFailureCursor{}, "evt-classification")
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].FailureCode != EventFanoutFailureCodeInvocationEnqueueFailed || !failures[0].Retryable {
		t.Fatalf("fanout failures = %+v, want invocation enqueue failure marked retryable", failures)
	}
}

func TestMemStoreReplayRetryablePublishedEventRecipientsIsBoundedAndAppScoped(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "fanout-replay-batch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-replay-batch"})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-replay-other"})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders.us", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders.*", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	permanent, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignRecipient, _, err := store.UpsertEventSubscription(ctx, accountID, otherApp.ID, "*", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-replay-batch","source":"orders.us","type":"order.created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	oldest := time.Now().UTC()
	for _, outcome := range []struct {
		subscription EventSubscription
		updatedAt    time.Time
		code         string
		retryable    bool
	}{
		{first, oldest, EventFanoutFailureCodeInvocationEnqueueFailed, true},
		{second, oldest.Add(time.Second), EventFanoutFailureCodeTargetLookupFailed, true},
		{permanent, oldest.Add(2 * time.Second), EventFanoutFailureCodeTargetUnavailable, false},
		{foreignRecipient, oldest.Add(3 * time.Second), EventFanoutFailureCodeInvocationEnqueueFailed, true},
	} {
		if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, outcome.subscription.ID,
			PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 12,
				FailureCode: outcome.code, Retryable: outcome.retryable, LastError: "route failed", UpdatedAt: outcome.updatedAt}); err != nil {
			t.Fatalf("record failure for %s: %v", outcome.subscription.ID, err)
		}
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}

	batch, err := store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, app.ID, "", "", 1)
	if err != nil || batch.Replayed != 1 || !batch.HasMore {
		t.Fatalf("first batch = %+v, %v; want one replay and has_more", batch, err)
	}
	work, err = store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := work.RecipientProgress[first.ID]; got.State != PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("oldest retryable progress = %+v, want cleared pending", got)
	}
	if got := work.RecipientProgress[second.ID]; got.State != PublishedEventRecipientFailed || !got.Retryable {
		t.Fatalf("second retryable progress = %+v, want unchanged failed", got)
	}
	if got := work.RecipientProgress[permanent.ID]; got.State != PublishedEventRecipientFailed || got.Retryable {
		t.Fatalf("non-retryable progress = %+v, want unchanged failed", got)
	}
	if got := work.RecipientProgress[foreignRecipient.ID]; got.State != PublishedEventRecipientFailed || !got.Retryable {
		t.Fatalf("other app progress = %+v, want unchanged failed", got)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}

	batch, err = store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, app.ID, "orders.us", "evt-replay-batch", 100)
	if err != nil || batch.Replayed != 1 || batch.HasMore {
		t.Fatalf("second batch = %+v, %v; want one replay and no more", batch, err)
	}
	work, err = store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := work.RecipientProgress[second.ID]; got.State != PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("second retryable progress = %+v, want cleared pending", got)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("settle second replay: %v", err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-replay-batch","source":"orders.eu","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event with colliding ID: %v", err)
	}
	work, err = store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event with colliding ID: %v", err)
	}
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, second.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 1,
			FailureCode: EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, LastError: "route failed", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record failure for event with colliding ID: %v", err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("settle event with colliding ID: %v", err)
	}

	batch, err = store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, app.ID, "orders.us", "evt-replay-batch", 100)
	if err != nil || batch.Replayed != 0 || batch.HasMore {
		t.Fatalf("source-qualified first event batch = %+v, %v; want no matching failures", batch, err)
	}
	batch, err = store.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, app.ID, "orders.eu", "evt-replay-batch", 100)
	if err != nil || batch.Replayed != 1 || batch.HasMore {
		t.Fatalf("source-qualified second event batch = %+v, %v; want one matching failure", batch, err)
	}
}

func TestMemStoreReplayFailedPublishedEventRecipientWaitsForReceiptToSettle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "fanout-replay-active@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-replay-active"})
	if err != nil {
		t.Fatal(err)
	}
	recipient, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-active","source":"orders","type":"order.created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID,
		PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 1, LastError: "target unavailable", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, errors.New("sibling retry remains")); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, app.ID, "orders", "evt-active", recipient.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay before receipt settles = %v, want ErrConflict", err)
	}
}

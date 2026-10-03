package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreReplayRetryablePublishedEventRecipientsIsBoundedAndAppScoped(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "fanout-replay-batch")
	otherApp, err := s.CreateApp(ctx, state.App{
		ID: uuid.NewString(), AccountID: accountID, Slug: "fanout-replay-other-" + uuid.NewString()[:8],
	})
	if err != nil {
		t.Fatalf("create second app: %v", err)
	}
	first, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders.us", "order.created", nil)
	if err != nil {
		t.Fatalf("create first subscription: %v", err)
	}
	second, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders.*", "order.created", nil)
	if err != nil {
		t.Fatalf("create second subscription: %v", err)
	}
	third, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders.us", "*", nil)
	if err != nil {
		t.Fatalf("create third subscription: %v", err)
	}
	permanent, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "*", "*", nil)
	if err != nil {
		t.Fatalf("create permanent subscription: %v", err)
	}
	other, _, err := s.UpsertEventSubscription(ctx, accountID, otherApp.ID, "*", "order.created", nil)
	if err != nil {
		t.Fatalf("create other-app subscription: %v", err)
	}
	if err := s.AppendEvent(ctx, "apid", "event.published", &accountID,
		json.RawMessage(`{"id":"evt-pg-replay-batch","source":"orders.us","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	work, err := s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event: %v", err)
	}
	oldest := time.Now().UTC().Truncate(time.Microsecond)
	for _, outcome := range []struct {
		subscription state.EventSubscription
		updatedAt    time.Time
		code         string
		retryable    bool
	}{
		{first, oldest, state.EventFanoutFailureCodeInvocationEnqueueFailed, true},
		{second, oldest.Add(time.Second), state.EventFanoutFailureCodeTargetLookupFailed, true},
		{third, oldest.Add(2 * time.Second), state.EventFanoutFailureCodeInvocationEnqueueFailed, true},
		{permanent, oldest.Add(3 * time.Second), state.EventFanoutFailureCodeTargetUnavailable, false},
		{other, oldest.Add(4 * time.Second), state.EventFanoutFailureCodeInvocationEnqueueFailed, true},
	} {
		if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, outcome.subscription.ID,
			state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 12,
				FailureCode: outcome.code, Retryable: outcome.retryable, LastError: "route failed", UpdatedAt: outcome.updatedAt}); err != nil {
			t.Fatalf("record failure for %s: %v", outcome.subscription.ID, err)
		}
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish event: %v", err)
	}

	batch, err := s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "", "", 1)
	if err != nil || batch.Replayed != 1 || !batch.HasMore {
		t.Fatalf("first batch = %+v, %v; want one replay and has_more", batch, err)
	}
	batch, err = s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "", "", 1)
	if err != nil || batch.Replayed != 1 || !batch.HasMore {
		t.Fatalf("second batch before fanout settles = %+v, %v; want one replay and has_more", batch, err)
	}
	work, err = s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim first replay: %v", err)
	}
	if got := work.RecipientProgress[first.ID]; got.State != state.PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("oldest retryable progress = %+v, want cleared pending", got)
	}
	if got := work.RecipientProgress[second.ID]; got.State != state.PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("second retryable progress = %+v, want cleared pending", got)
	}
	if got := work.RecipientProgress[third.ID]; got.State != state.PublishedEventRecipientFailed || !got.Retryable {
		t.Fatalf("third retryable progress = %+v, want unchanged failed", got)
	}
	if got := work.RecipientProgress[permanent.ID]; got.State != state.PublishedEventRecipientFailed || got.Retryable {
		t.Fatalf("non-retryable progress = %+v, want unchanged failed", got)
	}
	if got := work.RecipientProgress[other.ID]; got.State != state.PublishedEventRecipientFailed || !got.Retryable {
		t.Fatalf("other app progress = %+v, want unchanged failed", got)
	}
	batch, err = s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "", "", 1)
	if err != nil || batch.Replayed != 0 || !batch.HasMore {
		t.Fatalf("batch during fanout = %+v, %v; want no replay while processing and has_more", batch, err)
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("settle first replay: %v", err)
	}
	firstHistory, err := s.ListEventFanoutAttemptsForApp(ctx, appID, 10, state.EventFanoutAttemptCursor{},
		"orders.us", "evt-pg-replay-batch", first.ID)
	if err != nil || len(firstHistory) != 2 || firstHistory[0].Action != state.EventFanoutAttemptActionReplay ||
		firstHistory[0].FailureCode != state.EventFanoutFailureCodeInvocationEnqueueFailed || firstHistory[0].LastError != "route failed" {
		t.Fatalf("bulk replay history = %+v, %v; want replay snapshot and original failure", firstHistory, err)
	}

	batch, err = s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "orders.us", "evt-pg-replay-batch", 100)
	if err != nil || batch.Replayed != 1 || batch.HasMore {
		t.Fatalf("third batch after fanout settles = %+v, %v; want one replay and no more", batch, err)
	}
	work, err = s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim second replay: %v", err)
	}
	if got := work.RecipientProgress[third.ID]; got.State != state.PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("third retryable progress = %+v, want cleared pending", got)
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("settle third replay: %v", err)
	}
	if err := s.AppendEvent(ctx, "apid", "event.published", &accountID,
		json.RawMessage(`{"id":"evt-pg-replay-batch","source":"orders.eu","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event with colliding ID: %v", err)
	}
	work, err = s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event with colliding ID: %v", err)
	}
	if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, second.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 1,
			FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, LastError: "route failed", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record failure for event with colliding ID: %v", err)
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("settle event with colliding ID: %v", err)
	}
	failures, err := s.ListEventFanoutFailuresForApp(ctx, appID, 10, state.EventFanoutFailureCursor{}, "orders.eu", "evt-pg-replay-batch")
	if err != nil {
		t.Fatalf("list source-qualified fanout failures: %v", err)
	}
	if len(failures) != 1 || failures[0].EventSource != "orders.eu" || failures[0].EventID != "evt-pg-replay-batch" {
		t.Fatalf("source-qualified fanout failures = %+v, want only orders.eu collision", failures)
	}
	failures, err = s.ListEventFanoutFailuresForApp(ctx, appID, 10, state.EventFanoutFailureCursor{}, "orders.us", "evt-pg-replay-batch")
	if err != nil {
		t.Fatalf("list first source-qualified fanout failures: %v", err)
	}
	if len(failures) != 1 || failures[0].EventSource != "orders.us" {
		t.Fatalf("first source-qualified fanout failures = %+v, want only orders.us collision", failures)
	}

	batch, err = s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "orders.us", "evt-pg-replay-batch", 100)
	if err != nil || batch.Replayed != 0 || batch.HasMore {
		t.Fatalf("source-qualified first event batch = %+v, %v; want no matching failures", batch, err)
	}
	batch, err = s.ReplayRetryablePublishedEventRecipientsForApp(ctx, accountID, appID, "orders.eu", "evt-pg-replay-batch", 100)
	if err != nil || batch.Replayed != 1 || batch.HasMore {
		t.Fatalf("source-qualified second event batch = %+v, %v; want one matching failure", batch, err)
	}
}

func TestPgStoreFanoutAttemptHistorySurvivesReplay(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "fanout-history")
	subscription, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders", "order.created", nil)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	eventID := "evt-history-" + uuid.NewString()
	if err := s.AppendEvent(ctx, "apid", "event.published", &accountID,
		json.RawMessage(`{"id":"`+eventID+`","source":"orders","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	work, err := s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event: %v", err)
	}
	if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, subscription.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 2,
			FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true,
			LastError: "temporary queue outage", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish failed event: %v", err)
	}
	if err := s.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, appID, "orders", eventID, subscription.ID); err != nil {
		t.Fatalf("replay recipient: %v", err)
	}
	work, err = s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim replay: %v", err)
	}
	if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, subscription.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: 3, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record replay success: %v", err)
	}
	if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish replay: %v", err)
	}

	history, err := s.ListEventFanoutAttemptsForApp(ctx, appID, 10, state.EventFanoutAttemptCursor{},
		"orders", eventID, subscription.ID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %+v, want success, replay, and original failure", history)
	}
	if got := history[0]; got.Action != state.EventFanoutAttemptActionAttempt || got.State != state.PublishedEventRecipientEnqueued || got.Attempts != 3 {
		t.Fatalf("latest history row = %+v, want successful fanout outcome", got)
	}
	if got := history[1]; got.Action != state.EventFanoutAttemptActionReplay || got.State != state.PublishedEventRecipientPending ||
		got.Attempts != 2 || got.FailureCode != state.EventFanoutFailureCodeInvocationEnqueueFailed || !got.Retryable ||
		got.LastError != "temporary queue outage" {
		t.Fatalf("replay history row = %+v, want preserved failure snapshot", got)
	}
	if got := history[2]; got.Action != state.EventFanoutAttemptActionAttempt || got.State != state.PublishedEventRecipientFailed ||
		got.Attempts != 2 || got.FailureCode != state.EventFanoutFailureCodeInvocationEnqueueFailed || !got.Retryable ||
		got.LastError != "temporary queue outage" {
		t.Fatalf("initial history row = %+v, want original failure", got)
	}
	page, err := s.ListEventFanoutAttemptsForApp(ctx, appID, 1,
		state.EventFanoutAttemptCursor{ID: history[0].ID}, "orders", eventID, subscription.ID)
	if err != nil || len(page) != 1 || page[0].ID != history[1].ID {
		t.Fatalf("history continuation page = %+v, %v; want replay row", page, err)
	}
}

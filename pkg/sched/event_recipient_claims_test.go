// adr: 618
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type recipientRouteFaultStore struct {
	*state.MemStore
	failures       map[string]error
	loseCompletion bool
	claimApps      map[string]string
}

func (s *recipientRouteFaultStore) ClaimDuePublishedEventRecipient(ctx context.Context, now time.Time) (*state.PublishedEventRecipientWork, error) {
	work, err := s.MemStore.ClaimDuePublishedEventRecipient(ctx, now)
	if err == nil {
		if s.claimApps == nil {
			s.claimApps = map[string]string{}
		}
		s.claimApps[work.Recipient.ID] = work.Recipient.AppID
	}
	return work, err
}

func (s *recipientRouteFaultStore) AdmitPublishedEventRecipient(ctx context.Context, claim state.PublishedEventRoutingClaim) (state.PublishedEventRoutingResult, error) {
	if err := s.failures[s.claimApps[claim.SubscriptionID]]; err != nil {
		return state.PublishedEventRoutingResult{}, &state.EventRecipientAdmissionError{FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, Err: err}
	}
	result, err := s.MemStore.AdmitPublishedEventRecipient(ctx, claim)
	if err == nil && s.loseCompletion && result.Progress.State == state.PublishedEventRecipientEnqueued {
		s.loseCompletion = false
		return result, errors.New("lost routing acknowledgement after commit")
	}
	return result, err
}

func (s *recipientRouteFaultStore) EnqueueInvocation(ctx context.Context, invocation state.Invocation) (state.Invocation, error) {
	if err := s.failures[invocation.AppID]; err != nil {
		return state.Invocation{}, err
	}
	return s.MemStore.EnqueueInvocation(ctx, invocation)
}

func (s *recipientRouteFaultStore) FinishPublishedEventRecipient(ctx context.Context, work *state.PublishedEventRecipientWork, progress state.PublishedEventRecipientProgress, next time.Time) error {
	if s.loseCompletion && progress.State == state.PublishedEventRecipientEnqueued {
		s.loseCompletion = false
		return errors.New("lost routing acknowledgement after enqueue")
	}
	return s.MemStore.FinishPublishedEventRecipient(ctx, work, progress, next)
}

// ADR-618: A successful consumer, a backoff-delayed consumer, and a failed consumer
// share one event. Selective replay must not inherit the sibling's backoff.
func TestEventRecipientRoutingSelectiveRecovery(t *testing.T) {
	ctx := context.Background()
	store := &recipientRouteFaultStore{MemStore: state.NewMemStore(), failures: map[string]error{}}
	account, err := store.CreateAccount(ctx, "recipient-routing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	apps := make([]state.App, 0, 3)
	subscriptions := make([]state.EventSubscription, 0, 3)
	for _, slug := range []string{"billing", "analytics", "email"} {
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		subscriptions = append(subscriptions, sub)
	}
	now := time.Now().UTC()
	envelope, err := (events.Envelope{ID: "evt-independent", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(accountID, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	store.failures[apps[1].ID] = errors.New("temporary analytics queue outage")
	store.failures[apps[2].ID] = state.ErrNotFound
	loop := (&Loop{engine: &Engine{store: store}, now: func() time.Time { return now }}).WithEventRecipientClaims(true)
	// Acceptance and routing use real clock values, so claim strictly after publish.
	now = time.Now().UTC().Add(time.Millisecond)
	loop.runEventFanoutSweep(ctx)
	for i, app := range apps {
		invocations, err := store.ListInvocationsForApp(ctx, app.ID)
		want := 0
		if i == 0 {
			want = 1
		}
		if err != nil || len(invocations) != want {
			t.Fatalf("%s invocations = %d, %v", app.Slug, len(invocations), err)
		}
	}
	delete(store.failures, apps[2].ID)
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, apps[2].ID, "orders", envelope.ID, subscriptions[2].ID); err != nil {
		t.Fatalf("replay email while analytics pending: %v", err)
	}
	// Recovery and restart continue even with adoption disabled. Delete the
	// subscription to verify accepted work still uses its captured recipient.
	if err := store.DeleteEventSubscription(ctx, subscriptions[2].ID, accountID, apps[2].ID); err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC().Add(time.Millisecond)
	loop = &Loop{engine: &Engine{store: store}, now: func() time.Time { return now }}
	loop.runEventFanoutSweep(ctx)
	for _, index := range []int{0, 2} {
		invocations, err := store.ListInvocationsForApp(ctx, apps[index].ID)
		if err != nil || len(invocations) != 1 {
			t.Fatalf("%s after selective replay = %d, %v", apps[index].Slug, len(invocations), err)
		}
	}
	if rows, _ := store.ListInvocationsForApp(ctx, apps[1].ID); len(rows) != 0 {
		t.Fatal("sibling backoff was bypassed")
	}
	delete(store.failures, apps[1].ID)
	store.loseCompletion = true
	now = now.Add(6 * time.Second)
	loop.runEventFanoutSweep(ctx)
	// The worker enqueued successfully but lost its checkpoint. Lease expiry
	// and a new scheduler must recover without enqueueing a second invocation.
	now = now.Add(state.PublishedEventLease + time.Second)
	loop = &Loop{engine: &Engine{store: store}, now: func() time.Time { return now }}
	loop.runEventFanoutSweep(ctx)
	for _, app := range apps {
		rows, err := store.ListInvocationsForApp(ctx, app.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s after lost checkpoint = %d, %v", app.Slug, len(rows), err)
		}
	}
}

// ADR-618: replay receives a fresh bounded retry budget, while history keeps
// counting attempts across generations and successful siblings stay settled.
func TestEventRecipientRoutingReplayRenewsExhaustedBudget(t *testing.T) {
	ctx := context.Background()
	store := &recipientRouteFaultStore{MemStore: state.NewMemStore(), failures: map[string]error{}}
	account, err := store.CreateAccount(ctx, "exhausted-recipient@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "exhausted", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := (events.Envelope{ID: "evt-budget", Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(accountID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Millisecond)
	loop := (&Loop{engine: &Engine{store: store}, now: func() time.Time { return now }}).WithEventRecipientClaims(true)
	store.failures[app.ID] = errors.New("queue unavailable")
	for range eventFanoutRecipientMaxAttempts {
		loop.runEventFanoutSweep(ctx)
		now = now.Add(301 * time.Second)
	}
	history, err := store.ListEventFanoutAttemptsForApp(ctx, app.ID, 20, state.EventFanoutAttemptCursor{}, "orders", envelope.ID, sub.ID)
	if err != nil || len(history) != eventFanoutRecipientMaxAttempts || history[0].State != state.PublishedEventRecipientFailed {
		t.Fatalf("exhausted routing = %+v, %v", history, err)
	}
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, app.ID, "orders", envelope.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	loop.runEventFanoutSweep(ctx)
	if _, err := store.ClaimDuePublishedEventRecipient(ctx, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay skipped its retry backoff: %v", err)
	}
	delete(store.failures, app.ID)
	now = now.Add(6 * time.Second)
	loop.runEventFanoutSweep(ctx)
	rows, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("exhausted recipient recovery = %+v, %v", rows, err)
	}
	history, err = store.ListEventFanoutAttemptsForApp(ctx, app.ID, 20, state.EventFanoutAttemptCursor{}, "orders", envelope.ID, sub.ID)
	if err != nil || history[0].State != state.PublishedEventRecipientEnqueued || history[0].Attempts != eventFanoutRecipientMaxAttempts+2 {
		t.Fatalf("replay lost lifetime attempt count = %+v, %v", history, err)
	}
}

// spec: §4.10
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func TestRoutePublishedEventMatchesFiltersAndIsolatesAccounts(t *testing.T) {
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })
	ctx := context.Background()
	store := state.NewMemStore()

	accountA, err := store.CreateAccount(ctx, "events-a@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	accountB, err := store.CreateAccount(ctx, "events-b@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	accountAID := mustCanonicalEventAccountID(t, accountA.ID)
	accountBID := mustCanonicalEventAccountID(t, accountB.ID)
	matchingApp, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountAID, Slug: "events-matching"})
	if err != nil {
		t.Fatalf("CreateApp matching: %v", err)
	}
	filteredApp, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountAID, Slug: "events-filtered"})
	if err != nil {
		t.Fatalf("CreateApp filtered: %v", err)
	}
	otherAccountApp, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountBID, Slug: "events-other-account"})
	if err != nil {
		t.Fatalf("CreateApp other account: %v", err)
	}

	if _, _, err := store.UpsertEventSubscription(ctx, accountAID, matchingApp.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription matching: %v", err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountAID, filteredApp.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":200}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription filtered: %v", err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountBID, otherAccountApp.ID,
		"billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`)); err != nil {
		t.Fatalf("UpsertEventSubscription other account: %v", err)
	}

	envelope := events.Envelope{
		SpecVersion:     events.CloudEventsSpecVersion,
		ID:              uuid.NewString(),
		Source:          "billing.stripe",
		Type:            "invoice.paid",
		Time:            time.Now().UTC(),
		DataContentType: events.JSONDataContentType,
		Data:            json.RawMessage(`{"amount":150,"invoice_id":"inv-1"}`),
		AccountID:       accountAID,
		Traceparent:     "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	loop := &Loop{engine: &Engine{store: store}}
	if err := loop.routePublishedEvent(ctx, string(payload)); err != nil {
		t.Fatalf("routePublishedEvent: %v", err)
	}

	matchingInvocations, err := store.ListInvocationsForApp(ctx, matchingApp.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp matching: %v", err)
	}
	if len(matchingInvocations) != 1 {
		t.Fatalf("matching invocations = %d, want 1", len(matchingInvocations))
	}
	invocation := matchingInvocations[0]
	if invocation.Source != state.InvocationAsyncInvoke || invocation.Method != "POST" || invocation.Path != "/" {
		t.Fatalf("invocation route = %q %q %q, want async POST /", invocation.Source, invocation.Method, invocation.Path)
	}
	if string(invocation.Payload) != string(payload) {
		t.Fatalf("invocation payload = %s, want %s", invocation.Payload, payload)
	}
	var headers map[string]string
	if err := json.Unmarshal(invocation.Headers, &headers); err != nil {
		t.Fatalf("decode invocation headers: %v", err)
	}
	if headers["x-gregale-event-id"] != envelope.ID ||
		headers["x-gregale-event-source"] != envelope.Source ||
		headers["x-gregale-event-type"] != envelope.Type {
		t.Fatalf("event headers = %+v, want id/source/type headers", headers)
	}
	if headers["traceparent"] != envelope.Traceparent ||
		headers["X-Gregale-Trace-Id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("event trace headers = %+v, want producer context", headers)
	}

	filteredInvocations, err := store.ListInvocationsForApp(ctx, filteredApp.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp filtered: %v", err)
	}
	if len(filteredInvocations) != 0 {
		t.Fatalf("filtered invocations = %d, want 0", len(filteredInvocations))
	}
	otherAccountInvocations, err := store.ListInvocationsForApp(ctx, otherAccountApp.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp other account: %v", err)
	}
	if len(otherAccountInvocations) != 0 {
		t.Fatalf("cross-account invocations = %d, want 0", len(otherAccountInvocations))
	}
}

func TestRoutePublishedEventIsIdempotentForRepeatedNotifications(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "events-idempotent@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "events-idempotent"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	subscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID,
		"orders", "order.created", nil)
	if err != nil {
		t.Fatalf("UpsertEventSubscription: %v", err)
	}

	envelope := events.Envelope{
		SpecVersion:     events.CloudEventsSpecVersion,
		ID:              uuid.NewString(),
		Source:          "orders",
		Type:            "order.created",
		Time:            time.Now().UTC(),
		DataContentType: events.JSONDataContentType,
		Data:            json.RawMessage(`{"order_id":"order-1"}`),
		AccountID:       accountID,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	loop := &Loop{engine: &Engine{store: store}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := loop.routePublishedEvent(ctx, string(payload)); err != nil {
			t.Fatalf("routePublishedEvent attempt %d: %v", attempt+1, err)
		}
	}

	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListInvocationsForApp: %v", err)
	}
	if len(invocations) != 1 {
		t.Fatalf("invocations = %d, want 1 for subscription %s", len(invocations), subscription.ID)
	}
	if invocations[0].ID == "" {
		t.Fatal("deterministic invocation ID is empty")
	}
}

func TestRoutePublishedEventIdentityIncludesSource(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "event-sources@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-sources"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "*", "created", nil); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: store}}
	for _, source := range []string{"first", "second"} {
		event := events.Envelope{SpecVersion: "1.0", ID: "same-id", Source: source, Type: "created", Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{}`), AccountID: accountID}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := loop.routePublishedEvent(ctx, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want 2", len(invocations))
	}
}

func TestEventFanoutSweepDrainsOldBacklog(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "event-backlog@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-backlog"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC()
	for i := 0; i < 1001; i++ {
		event := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "orders", Type: "created", Time: old, DataContentType: "application/json", Data: json.RawMessage(`{}`), AccountID: accountID}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
			t.Fatal(err)
		}
	}
	loop := &Loop{engine: &Engine{store: store}, now: func() time.Time { return old.Add(24 * time.Hour) }}
	for i := 0; i < 11; i++ {
		loop.runEventFanoutSweep(ctx)
	}
	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 1001 {
		t.Fatalf("invocations = %d, want 1001", len(invocations))
	}
	if _, err := store.ClaimDuePublishedEvent(ctx, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("remaining outbox claim: %v", err)
	}
}

func TestEventFanoutDoesNotBackfillNewSubscription(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "event-late-subscription@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-late-subscription"})
	if err != nil {
		t.Fatal(err)
	}
	event := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "orders", Type: "created", Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{}`), AccountID: accountID}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: store}}
	loop.runEventFanoutSweep(ctx)
	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 0 {
		t.Fatalf("late subscription received %d old events", len(invocations))
	}
}

func TestEventFanoutUsesSubscriptionAtAcceptance(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "event-snapshot@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	oldSubscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID,
		"orders.*", "created", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`))
	if err != nil {
		t.Fatal(err)
	}
	event := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "orders.api", Type: "created",
		Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{"amount":150}`), AccountID: accountID}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEventSubscription(ctx, oldSubscription.ID, accountID, app.ID); err != nil {
		t.Fatal(err)
	}
	newSubscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID,
		"orders.*", "created", json.RawMessage(`{"data":{"amount":{"$gt":200}}}`))
	if err != nil {
		t.Fatal(err)
	}
	// Retrying the same event after changing subscriptions must keep the
	// original candidate set rather than replacing the existing receipt.
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: store}}
	loop.runEventFanoutSweep(ctx)
	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 1 {
		t.Fatalf("invocations = %d, want one accepted recipient", len(invocations))
	}
	var headers map[string]string
	if err := json.Unmarshal(invocations[0].Headers, &headers); err != nil {
		t.Fatal(err)
	}
	if headers["x-gregale-event-subscription-id"] != oldSubscription.ID || headers["x-gregale-event-subscription-id"] == newSubscription.ID {
		t.Fatalf("routed to subscription %q, want accepted subscription %q", headers["x-gregale-event-subscription-id"], oldSubscription.ID)
	}
}

// A pre-migration receipt has no snapshot and keeps the prior routing rule.
type legacyEventReceiptStore struct {
	state.Store
	mem *state.MemStore
}

func (s legacyEventReceiptStore) ClaimDuePublishedEvent(ctx context.Context, now time.Time) (*state.PublishedEventWork, error) {
	work, err := s.mem.ClaimDuePublishedEvent(ctx, now)
	if err == nil {
		work.SnapshotCaptured = false
		work.RecipientSnapshot = nil
	}
	return work, err
}

func (s legacyEventReceiptStore) FinishPublishedEvent(ctx context.Context, id int64, token string, routeErr error) error {
	return s.mem.FinishPublishedEvent(ctx, id, token, routeErr)
}

func (s legacyEventReceiptStore) ListMatchingEventSubscriptionsForAccount(ctx context.Context, accountID, source, typ string, cursor state.EventSubscriptionCursor, limit int) ([]state.EventSubscription, error) {
	return s.mem.ListMatchingEventSubscriptionsForAccount(ctx, accountID, source, typ, cursor, limit)
}

func TestEventFanoutLegacyReceiptKeepsCurrentSubscriptionRouting(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "event-legacy-snapshot@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := mem.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-legacy-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	subscription, _, err := mem.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil)
	if err != nil {
		t.Fatal(err)
	}
	event := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "orders", Type: "created",
		Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{}`), AccountID: accountID}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	if err := mem.DeleteEventSubscription(ctx, subscription.ID, accountID, app.ID); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: legacyEventReceiptStore{Store: mem, mem: mem}}}
	loop.runEventFanoutSweep(ctx)
	invocations, err := mem.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 0 {
		t.Fatalf("pre-migration receipt delivered to removed subscription: %d invocations", len(invocations))
	}
	if _, err := mem.ClaimDuePublishedEvent(ctx, time.Now().UTC()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy receipt was not acknowledged: %v", err)
	}
}

type eventFanoutEnqueueFailureStore struct {
	*state.MemStore
}

func (s eventFanoutEnqueueFailureStore) EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error) {
	return state.Invocation{}, errors.New("temporary enqueue outage")
}

func TestEventFanoutClassifiesTerminalRecipientFailures(t *testing.T) {
	t.Run("invalid subscription", func(t *testing.T) {
		ctx := context.Background()
		store := state.NewMemStore()
		account, err := store.CreateAccount(ctx, "event-invalid-filter@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := mustCanonicalEventAccountID(t, account.ID)
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-invalid-filter"})
		if err != nil {
			t.Fatal(err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
			[]byte(`{"id":"evt-invalid-filter","source":"orders","type":"created","data":{}}`)); err != nil {
			t.Fatal(err)
		}
		work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		work.RecipientSnapshot[0].Filter = json.RawMessage(`[]`)
		loop := &Loop{engine: &Engine{store: store}}
		if err := loop.routePublishedEventSnapshot(ctx, work); err != nil {
			t.Fatalf("route snapshot: %v", err)
		}
		progress := work.RecipientProgress[subscription.ID]
		if progress.State != state.PublishedEventRecipientFailed ||
			progress.FailureCode != state.EventFanoutFailureCodeInvalidSubscription || progress.Retryable {
			t.Fatalf("recipient progress = %+v, want invalid_subscription and retryable=false", progress)
		}
	})

	t.Run("target unavailable", func(t *testing.T) {
		ctx := context.Background()
		store := state.NewMemStore()
		account, err := store.CreateAccount(ctx, "event-missing-target@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := mustCanonicalEventAccountID(t, account.ID)
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-missing-target"})
		if err != nil {
			t.Fatal(err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &accountID,
			[]byte(`{"id":"evt-missing-target","source":"orders","type":"created","data":{}}`)); err != nil {
			t.Fatal(err)
		}
		work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		work.RecipientSnapshot[0].AppID = uuid.NewString()
		loop := &Loop{engine: &Engine{store: store}}
		if err := loop.routePublishedEventSnapshot(ctx, work); err != nil {
			t.Fatalf("route snapshot: %v", err)
		}
		progress := work.RecipientProgress[subscription.ID]
		if progress.State != state.PublishedEventRecipientFailed ||
			progress.FailureCode != state.EventFanoutFailureCodeTargetUnavailable || progress.Retryable {
			t.Fatalf("recipient progress = %+v, want target_unavailable and retryable=false", progress)
		}
	})
}

func TestEventFanoutMarksTransientEnqueueFailureRetryableAfterExhaustion(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "event-transient-failure@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := mem.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-transient-failure"})
	if err != nil {
		t.Fatal(err)
	}
	subscription, _, err := mem.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.AppendEvent(ctx, "apid", "event.published", &accountID,
		[]byte(`{"id":"evt-transient-failure","source":"orders","type":"created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := mem.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: eventFanoutEnqueueFailureStore{MemStore: mem}}}
	for attempt := 1; attempt <= eventFanoutRecipientMaxAttempts; attempt++ {
		err := loop.routePublishedEventSnapshot(ctx, work)
		if attempt < eventFanoutRecipientMaxAttempts && err == nil {
			t.Fatalf("attempt %d error = nil, want retryable enqueue error", attempt)
		}
		if attempt == eventFanoutRecipientMaxAttempts && err != nil {
			t.Fatalf("terminal attempt error = %v, want recipient failure captured without retrying receipt", err)
		}
	}
	progress := work.RecipientProgress[subscription.ID]
	if progress.State != state.PublishedEventRecipientFailed || progress.Attempts != eventFanoutRecipientMaxAttempts ||
		progress.FailureCode != state.EventFanoutFailureCodeInvocationEnqueueFailed || !progress.Retryable {
		t.Fatalf("recipient progress = %+v, want exhausted invocation enqueue failure marked retryable", progress)
	}
}

func mustCanonicalEventAccountID(t *testing.T, id string) string {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse account ID %q: %v", id, err)
	}
	return parsed.String()
}

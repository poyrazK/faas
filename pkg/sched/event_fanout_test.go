package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRoutePublishedEventMatchesFiltersAndIsolatesAccounts(t *testing.T) {
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

func mustCanonicalEventAccountID(t *testing.T, id string) string {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse account ID %q: %v", id, err)
	}
	return parsed.String()
}

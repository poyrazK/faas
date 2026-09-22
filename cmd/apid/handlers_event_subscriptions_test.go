package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListEventDeliveries_ReturnsOnlyEventInvocations(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "delivery-app")
	now := time.Now().UTC()
	for _, inv := range []state.Invocation{
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationCompleted, Headers: json.RawMessage(`{"x-gregale-event-id":"evt-1","x-gregale-event-source":"billing","x-gregale-event-type":"invoice.paid","x-gregale-event-subscription-id":"sub-1"}`),
			DueAt: now, CreatedAt: now, Attempts: 1,
		},
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationFailed, Headers: json.RawMessage(`{"x-gregale-event-id":"evt-2","x-gregale-event-source":"billing","x-gregale-event-type":"invoice.failed"}`),
			DueAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Second), Attempts: 3, LastError: "worker unavailable",
		},
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationCompleted, Headers: json.RawMessage(`{"x-user":"ordinary"}`),
			DueAt: now.Add(-2 * time.Second), CreatedAt: now.Add(-2 * time.Second),
		},
	} {
		if _, err := e.store.EnqueueInvocation(context.Background(), inv); err != nil {
			t.Fatalf("enqueue invocation: %v", err)
		}
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?state=failed", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.EventDeliveryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Deliveries) != 1 || out.Deliveries[0].EventID != "evt-2" || out.Deliveries[0].Attempts != 3 {
		t.Fatalf("deliveries = %+v, want filtered failed event", out.Deliveries)
	}
	if out.Deliveries[0].LastError != "worker unavailable" {
		t.Fatalf("last_error = %q", out.Deliveries[0].LastError)
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("event filter status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode event filter: %v", err)
	}
	if len(out.Deliveries) != 1 || out.Deliveries[0].SubscriptionID != "sub-1" {
		t.Fatalf("event filter deliveries = %+v", out.Deliveries)
	}

	// A cursor is positioned in the unfiltered event stream. Changing the
	// filter must not restart pagination when the cursor row is excluded.
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?limit=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 2 {
		t.Fatalf("decode page: err=%v deliveries=%+v", err, out.Deliveries)
	}
	if out.Deliveries[1].EventID != "evt-2" {
		t.Fatalf("cursor event = %q, want evt-2", out.Deliveries[1].EventID)
	}
	before := out.Deliveries[1].InvocationID
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&before="+before, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered cursor status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 0 {
		t.Fatalf("filtered cursor: err=%v deliveries=%+v, want empty page", err, out.Deliveries)
	}
}

func TestListEventDeliveries_CrossAccountIsNotVisible(t *testing.T) {
	owner := setup(t, api.PlanPro)
	appID := mustSeedApp(t, owner, "private-deliveries")
	if _, err := owner.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: appID, AccountID: owner.acct.ID, Source: state.InvocationAsyncInvoke,
		Headers: json.RawMessage(`{"x-gregale-event-id":"evt-private"}`), DueAt: time.Now(), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue invocation: %v", err)
	}
	foreignAcct, err := owner.store.CreateAccount(context.Background(), "foreign-deliveries@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	foreignToken, foreignHash, _ := api.GenerateAPIKey()
	if _, err := owner.store.CreateAPIKey(context.Background(), foreignAcct.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("create foreign key: %v", err)
	}
	foreign := owner
	foreign.acct = foreignAcct
	foreign.key = foreignToken
	rec := foreign.do(t, http.MethodGet, "/v1/apps/private-deliveries/event-deliveries", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListEventSubscriptions_ReturnsReconciledManifestRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "events-app")
	_, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID,
		"orders", "order.created", json.RawMessage(`{"status":"paid"}`))
	if err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}

	rec := e.do(t, "GET", "/v1/apps/events-app/event-subscriptions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.EventSubscriptionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AppSlug != "events-app" {
		t.Fatalf("app_slug = %q, want events-app", out.AppSlug)
	}
	if len(out.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(out.Subscriptions))
	}
	got := out.Subscriptions[0]
	if got.Source != "orders" || got.Type != "order.created" || !got.Enabled {
		t.Fatalf("subscription = %+v", got)
	}
	if string(got.Filter) != `{"status":"paid"}` {
		t.Fatalf("filter = %s, want normalized object", got.Filter)
	}
}

func TestListEventSubscriptions_CrossAccountIsNotVisible(t *testing.T) {
	first := setup(t, api.PlanPro)
	appID := mustSeedApp(t, first, "private-events-app")
	if _, _, err := first.store.UpsertEventSubscription(context.Background(), first.acct.ID, appID,
		"orders", "order.created", nil); err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}

	secondAcct, err := first.store.CreateAccount(context.Background(), "other-events@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create second account: %v", err)
	}
	secondToken, secondHash, _ := api.GenerateAPIKey()
	if _, err := first.store.CreateAPIKey(context.Background(), secondAcct.ID, secondHash, "second", api.ScopesAdminOnly); err != nil {
		t.Fatalf("create second key: %v", err)
	}
	second := first
	second.acct = secondAcct
	second.key = secondToken

	rec := second.do(t, "GET", "/v1/apps/private-events-app/event-subscriptions", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

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

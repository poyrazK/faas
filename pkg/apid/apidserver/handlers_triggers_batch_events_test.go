package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1342: batch-create applies the target app's event_triggers the way a
// source deploy does, so a functions manifest can subscribe a function
// after its deployment. A repeat call keeps one subscription, and
// declarations for another app are skipped.
func TestBatchCreateTriggerAppliesEventTriggers(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "shop-users", Type: state.AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	manifest := `event_triggers:
  - app: shop-users
    source: billing.*
    type: invoice.paid
  - app: shop-other
    source: billing.*
    type: invoice.void
`
	for attempt := range 2 {
		rec := e.Do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{AppID: app.ID, ManifestYAML: manifest}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d: %s", attempt, rec.Code, rec.Body.String())
		}
		var out apidserver.BatchCreateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Errors) != 0 {
			t.Fatalf("attempt %d: errors = %+v", attempt, out.Errors)
		}
		subs, err := (*e.Store()).ListEventSubscriptionsForApp(t.Context(), app.ID)
		if err != nil {
			t.Fatalf("ListEventSubscriptionsForApp: %v", err)
		}
		if len(subs) != 1 || subs[0].Source != "billing.*" || subs[0].Type != "invoice.paid" {
			t.Fatalf("attempt %d: subscriptions = %+v, want only billing.*/invoice.paid", attempt, subs)
		}
	}
}

// A Free account cannot declare event triggers through batch-create either.
func TestBatchCreateTriggerEventTriggersNeedTriggerPlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "free-fn", Type: state.AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{
		AppID:        app.ID,
		ManifestYAML: "event_triggers:\n  - source: billing.*\n    type: invoice.paid\n",
	}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402: %s", rec.Code, rec.Body.String())
	}
}

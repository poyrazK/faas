package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Acceptance for the event-delivery-controls capability: an app pauses a
// subscription's deliveries, reads the control back, and resumes them at a
// bounded drain rate. Another app's subscription is not reachable.
func TestEventDeliveryControlsPauseAndResume(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()
	for _, slug := range []string{"orders-worker", "other-worker"} {
		if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug, RAMMB: 256}, nil); r.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, r.Code, r.Body)
		}
	}
	app, err := store.AppBySlug(ctx, "orders-worker")
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, acct.ID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/orders-worker/event-subscriptions/" + sub.ID + "/delivery-control"
	control := func(method, path string, body any) api.EventSubscriptionDeliveryControl {
		t.Helper()
		r := e.Do(t, method, path, body, nil)
		var out api.EventSubscriptionDeliveryControl
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatalf("%s %s = %d %s", method, path, r.Code, r.Body)
		}
		return out
	}
	if got := control(http.MethodGet, base, nil); got.Paused || got.SubscriptionID != sub.ID {
		t.Fatalf("initial control = %+v", got)
	}
	if got := control(http.MethodPost, base+"/pause", nil); !got.Paused {
		t.Fatalf("after pause = %+v", got)
	}
	if got := control(http.MethodGet, base, nil); !got.Paused {
		t.Fatalf("read after pause = %+v", got)
	}
	rate := 5
	if got := control(http.MethodPost, base+"/resume", api.EventSubscriptionResumeRequest{RatePerSecond: &rate}); got.Paused || got.RatePerSecond != rate {
		t.Fatalf("after resume = %+v", got)
	}
	tooFast := 101
	if r := e.Do(t, http.MethodPost, base+"/resume", api.EventSubscriptionResumeRequest{RatePerSecond: &tooFast}, nil); r.Code != http.StatusUnprocessableEntity && r.Code != http.StatusBadRequest {
		t.Fatalf("resume above the rate bound = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/apps/other-worker/event-subscriptions/"+sub.ID+"/delivery-control/pause", nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("pause through another app = %d %s", r.Code, r.Body)
	}
}

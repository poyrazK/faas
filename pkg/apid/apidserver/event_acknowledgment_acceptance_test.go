package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Acceptance for the event-acknowledgments capability: a subscription opts
// into deferred acknowledgment, a delivery is offered and accepted the way
// schedd and the handler do, and the app then reads, renews and completes it
// through the public routes. A replayed completion with different content is
// refused as stale.
func TestEventAcknowledgmentLifecycle(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "ack-worker", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	app, err := store.AppBySlug(ctx, "ack-worker")
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, acct.ID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := "/v1/apps/ack-worker/event-subscriptions/" + sub.ID + "/acknowledgment-policy"
	lease := 60
	if r := e.Do(t, http.MethodPut, policyPath, api.EventAcknowledgmentPolicyRequest{LeaseSeconds: &lease}, nil); r.Code != http.StatusOK {
		t.Fatalf("set policy: %d %s", r.Code, r.Body)
	}
	var policy api.EventAcknowledgmentPolicy
	if r := e.Do(t, http.MethodGet, policyPath, nil, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &policy) != nil || policy.LeaseSeconds != lease {
		t.Fatalf("get policy = %d %s", r.Code, r.Body)
	}

	// schedd stamps the accepted subscription's lease on the delivery,
	// claims it, offers a token and the handler accepts it.
	headers, _ := json.Marshal(map[string]string{
		"x-gregale-event-acknowledgment-lease-seconds": "60",
		"x-gregale-event-acceptance-id":                "1",
		"x-gregale-event-subscription-id":              sub.ID,
	})
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: acct.ID,
		Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: http.MethodPost, Path: "/",
		Payload: json.RawMessage(`{"id":"evt-1"}`), Headers: headers, CreatedAt: time.Now(), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	var acks state.EventAcknowledgmentStore = store
	token, err := acks.OfferEventAcknowledgment(ctx, claimed, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acks.AcceptEventAcknowledgment(ctx, claimed, token); err != nil {
		t.Fatal(err)
	}

	ackPath := "/v1/apps/ack-worker/event-deliveries/" + inv.ID + "/acknowledgment"
	status := func(r interface{ Bytes() []byte }) api.EventAcknowledgmentStatus {
		t.Helper()
		var out api.EventAcknowledgmentStatus
		if err := json.Unmarshal(r.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	r := e.Do(t, http.MethodGet, ackPath, nil, nil)
	if r.Code != http.StatusOK || status(r.Body).ProcessingState != "processing" {
		t.Fatalf("get acknowledgment = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, ackPath+"/renew", api.EventAcknowledgmentRequest{Token: "not-the-token"}, nil); r.Code != http.StatusConflict && r.Code != http.StatusUnprocessableEntity && r.Code != http.StatusBadRequest {
		t.Fatalf("renew with a foreign token = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, ackPath+"/renew", api.EventAcknowledgmentRequest{Token: token}, nil); r.Code != http.StatusOK {
		t.Fatalf("renew = %d %s", r.Code, r.Body)
	}
	complete := api.EventAcknowledgmentRequest{Token: token, Result: json.RawMessage(`{"ok":true}`)}
	if r := e.Do(t, http.MethodPost, ackPath+"/complete", complete, nil); r.Code != http.StatusOK {
		t.Fatalf("complete = %d %s", r.Code, r.Body)
	}
	if got, err := store.InvocationByID(ctx, inv.ID); err != nil || got.State != state.InvocationCompleted {
		t.Fatalf("invocation after completion = %+v %v", got, err)
	}
	// A retried completion with the same content is idempotent; different
	// content is refused.
	if r := e.Do(t, http.MethodPost, ackPath+"/complete", complete, nil); r.Code != http.StatusOK {
		t.Fatalf("repeated complete = %d %s", r.Code, r.Body)
	}
	complete.Result = json.RawMessage(`{"ok":false}`)
	if r := e.Do(t, http.MethodPost, ackPath+"/complete", complete, nil); r.Code != http.StatusConflict {
		t.Fatalf("complete with different content = %d %s", r.Code, r.Body)
	}
}

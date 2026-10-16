package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSendAppMessageEnqueuesCloudEvent(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "billing")

	rec := e.Do(t, http.MethodPost, "/v1/apps/billing/inbox", api.SendAppMessageRequest{
		Type:      "invoice.created",
		Data:      json.RawMessage(`{"invoice_id":"inv_123"}`),
		QueueName: "billing-work",
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.SendAppMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.EventID == "" || response.TargetApp != "billing" || response.StatusURL == "" {
		t.Fatalf("unexpected response: %+v", response)
	}
	inv, err := (*e.Store()).InvocationByID(t.Context(), response.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if inv.Source != state.InvocationQueue || inv.QueueName != "billing-work" {
		t.Fatalf("invocation route = source %q queue %q", inv.Source, inv.QueueName)
	}
	var envelope events.Envelope
	if err := json.Unmarshal(inv.Payload, &envelope); err != nil {
		t.Fatalf("decode queued envelope: %v", err)
	}
	if envelope.SpecVersion != events.CloudEventsSpecVersion || envelope.ID != response.EventID || envelope.Source != "gregale.send" || envelope.Type != "invoice.created" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if string(envelope.Data) != `{"invoice_id":"inv_123"}` {
		t.Fatalf("data = %s", envelope.Data)
	}
}

func TestDeliverAppEventEnqueuesRegisteredWebhook(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "checkout-api")
	hook := mustCreateWebhook(t, e, "checkout-api", webhookReq())

	rec := e.Do(t, http.MethodPost, "/v1/apps/checkout-api/outbox", api.DeliverAppEventRequest{
		Destination: hook.TargetURL,
		Type:        "order.paid",
		Data:        json.RawMessage(`{"order_id":"ord_123"}`),
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.DeliverAppEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.WebhookID != hook.ID || response.Event != "order.paid" || response.Status != "pending" || response.StatusURL == "" {
		t.Fatalf("unexpected response: %+v", response)
	}
	delivery, err := (*e.Store()).AppWebhookDeliveryByID(t.Context(), response.ID)
	if err != nil {
		t.Fatalf("AppWebhookDeliveryByID: %v", err)
	}
	if delivery.WebhookID != hook.ID || delivery.Event != state.AppWebhookEvent("order.paid") || delivery.Status != state.AppWebhookDeliveryPending {
		t.Fatalf("unexpected delivery: %+v", delivery)
	}
	if string(delivery.Payload) != `{"order_id":"ord_123"}` {
		t.Fatalf("payload = %s", delivery.Payload)
	}
}

func TestDeliverAppEventRejectsUnregisteredDestination(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "checkout-api")
	rec := e.Do(t, http.MethodPost, "/v1/apps/checkout-api/outbox", api.DeliverAppEventRequest{
		Destination: "https://unregistered.example/hook",
		Type:        "order.paid",
		Data:        json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// production-us hunt #4 (H4-16): the detail must say the URL is not a
	// registered webhook and how to register it.
	for _, want := range []string{"https://unregistered.example/hook", "gregale webhooks add --app checkout-api"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("problem body %s missing %q", rec.Body.String(), want)
		}
	}
}

// TestDeliverAppEventRetryAfterRegisteringIsNotReplayed covers H10-54: a
// deliver to a destination registered only after the first attempt kept
// replaying the cached 404 for the same Idempotency-Key for 24 hours.
func TestDeliverAppEventRetryAfterRegisteringIsNotReplayed(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "checkout-api")
	hookReq := webhookReq()
	deliver := func() int {
		return e.Do(t, http.MethodPost, "/v1/apps/checkout-api/outbox", api.DeliverAppEventRequest{
			Destination: hookReq.TargetURL,
			Type:        "order.paid",
			Data:        json.RawMessage(`{"order_id":"ord_123"}`),
		}, map[string]string{"Idempotency-Key": "deliver-after-register"}).Code
	}
	if code := deliver(); code != http.StatusNotFound {
		t.Fatalf("first deliver = %d, want 404 before the webhook exists", code)
	}
	mustCreateWebhook(t, e, "checkout-api", hookReq)
	if code := deliver(); code != http.StatusAccepted {
		t.Fatalf("deliver after registering = %d, want 202 (the cached 404 must not replay)", code)
	}
}

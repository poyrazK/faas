package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSendAppMessageEnqueuesCloudEvent(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "billing")

	rec := e.do(t, http.MethodPost, "/v1/apps/billing/inbox", api.SendAppMessageRequest{
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
	inv, err := e.store.InvocationByID(t.Context(), response.ID)
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
	mustSeedApp(t, e, "checkout-api")
	hook := mustCreateWebhook(t, e, "checkout-api", webhookReq())

	rec := e.do(t, http.MethodPost, "/v1/apps/checkout-api/outbox", api.DeliverAppEventRequest{
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
	delivery, err := e.store.AppWebhookDeliveryByID(t.Context(), response.ID)
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
	mustSeedApp(t, e, "checkout-api")
	rec := e.do(t, http.MethodPost, "/v1/apps/checkout-api/outbox", api.DeliverAppEventRequest{
		Destination: "https://unregistered.example/hook",
		Type:        "order.paid",
		Data:        json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

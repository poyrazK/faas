package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientWebhookAutomationRoutes(t *testing.T) {
	var seen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("authorization absent")
		}
		switch r.Method + " " + r.URL.Path {
		case "PUT /v1/apps/billing/inbound-webhooks/endpoint/automation-binding":
			var body PutWebhookAutomationBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedVersion == nil || *body.ExpectedVersion != 0 || !body.TakeOverDelivery {
				t.Error("binding body", body, err)
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("idempotency absent")
			}
			_, _ = w.Write([]byte(`{"endpoint_id":"endpoint","workflow_name":"paid","event_type":"invoice.*","filter":{},"version":7,"updated_at":"2026-10-03T12:00:00Z"}`))
		case "GET /v1/apps/billing/inbound-webhooks/endpoint/automation-binding":
			_, _ = w.Write([]byte(`{"endpoint_id":"endpoint","workflow_name":"paid","event_type":"invoice.*","filter":{},"version":7,"updated_at":"2026-10-03T12:00:00Z"}`))
		case "GET /v1/apps/billing/inbound-webhooks/endpoint/automation-receipts/evt_1":
			_, _ = w.Write([]byte(`{"receipt_id":"receipt","endpoint_id":"endpoint","provider_event_id":"evt_1","workflow_name":"paid","status":"accepted","duplicate":false,"accepted_at":"2026-10-03T12:00:00Z","event_source":"gregale.inbound.stripe.endpoint","routing_status":"enqueued","run_id":"run"}`))
		case "DELETE /v1/apps/billing/inbound-webhooks/endpoint/automation-binding":
			if r.URL.Query().Get("expected_version") != "7" {
				t.Error("delete revision absent")
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	ctx := context.Background()
	zero := int64(0)
	binding, err := client.PutWebhookAutomationBinding(ctx, "billing", "endpoint", PutWebhookAutomationBindingRequest{ExpectedVersion: &zero, WorkflowName: "paid", EventType: "invoice.*", TakeOverDelivery: true})
	if err != nil || binding.Version != 7 {
		t.Fatal(binding, err)
	}
	if binding, err = client.GetWebhookAutomationBinding(ctx, "billing", "endpoint"); err != nil || binding.WorkflowName != "paid" {
		t.Fatal(binding, err)
	}
	receipt, err := client.GetWebhookAutomationReceipt(ctx, "billing", "endpoint", "evt_1")
	if err != nil || receipt.RunID != "run" {
		t.Fatal(receipt, err)
	}
	if err := client.DeleteWebhookAutomationBinding(ctx, "billing", "endpoint", 7); err != nil {
		t.Fatal(err)
	}
	if seen != 4 {
		t.Fatalf("requests=%d", seen)
	}
}

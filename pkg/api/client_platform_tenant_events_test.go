package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformTenantSelfEventClientRoutes(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch requests {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/platform-tenant-self/apps/my-app/events:publish" {
				t.Errorf("publish route = %s %s", r.Method, r.URL.String())
			}
			var body PublishEventRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID != "client-1" {
				t.Errorf("publish body = %+v, err=%v", body, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"canonical-id","client_event_id":"client-1","accepted_at":"2026-10-06T00:00:00Z","receipt_url":"/receipt"}`))
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/platform-tenant-self/apps/my-app/events/receipts/canonical-id" ||
				r.URL.Query().Get("source") != "billing.stripe" || r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("after") != "cursor" {
				t.Errorf("receipt route = %s %s", r.Method, r.URL.String())
			}
			_, _ = w.Write([]byte(`{"event_id":"canonical-id","event_source":"billing.stripe","event_type":"invoice.paid","accepted_at":"2026-10-06T00:00:00Z","snapshot_captured":true,"routing_mode":"event","recipient_count":0,"routing_summary":{},"recipients":[]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "tenant-token")
	accepted, err := client.PublishPlatformTenantSelfEvent(context.Background(), "my-app", PublishEventRequest{ID: "client-1", Source: "billing.stripe", Type: "invoice.paid", Data: json.RawMessage(`{}`)})
	if err != nil || accepted.ID != "canonical-id" || accepted.ClientEventID != "client-1" {
		t.Fatalf("publish response = %+v, err=%v", accepted, err)
	}
	receipt, err := client.GetPlatformTenantSelfEventReceipt(context.Background(), "my-app", accepted.ID, "billing.stripe", 25, "cursor")
	if err != nil || receipt.EventID != accepted.ID || requests != 2 {
		t.Fatalf("receipt response = %+v, err=%v requests=%d", receipt, err, requests)
	}
}

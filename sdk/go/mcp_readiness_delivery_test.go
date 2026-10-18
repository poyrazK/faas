package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMCPReadinessDestinationDelivery(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	id := "00000000-0000-0000-0000-000000000001"
	version := int64(0)
	request := MCPReadinessNotificationAction{RequestID: id, EventID: id, Destination: "primary", Action: "retry", ExpectedVersion: &version, Reason: "Receiver repaired"}
	head := &MCPReadinessIncidentEvent{ID: id, EventID: id, IncidentID: id, RuleID: id, Event: "alert.resolved", OccurredAt: at, IncidentStatus: "resolved", DeliveryStatus: "failed", DestinationDeliveries: []MCPReadinessDestinationDelivery{{Destination: "primary", Status: "failed", AttemptCount: 1, NextAttemptAt: &at}, {Destination: "fallback", Status: "delivered", AttemptCount: 1}}}
	page := MCPReadinessNotificationQueues{ObservedAt: at, Queues: []MCPReadinessNotificationQueue{{RuleID: id, Name: "Readiness", Enabled: true, OutstandingCount: 1, Head: head, Destinations: []MCPReadinessDestinationQueue{{Destination: "primary", State: "delayed", OutstandingCount: 1, CanRetry: true, CanAbandon: true, Head: head}}}}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authorization")
		}
		switch r.URL.Path {
		case "/v1/apps/demo/mcp-readiness-notifications":
			if r.URL.Query().Get("scope") != "staging" || r.URL.Query().Get("limit") != "20" {
				t.Error("lost queue filters")
			}
			_ = json.NewEncoder(w).Encode(page)
		case "/v1/apps/demo/mcp-readiness-notifications/actions":
			var body MCPReadinessNotificationAction
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Destination != "primary" || body.ExpectedVersion == nil || *body.ExpectedVersion != 0 {
				t.Error("lost destination or zero version", body, err)
			}
			_ = json.NewEncoder(w).Encode(MCPReadinessNotificationReceipt{RequestID: id, EventID: id, Destination: body.Destination, Action: body.Action, DeliveryVersion: 1})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListMCPReadinessNotificationQueues(context.Background(), "demo", "staging", "", 20)
	if err != nil || len(got.Queues) != 1 || got.Queues[0].Destinations[0].Head.DestinationDeliveries[1].Status != "delivered" {
		t.Fatal(got, err)
	}
	receipt, err := client.ActMCPReadinessNotification(context.Background(), "demo", request)
	if err != nil || receipt.Destination != "primary" || receipt.DeliveryVersion != 1 {
		t.Fatal(receipt, err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestQueueEnvironmentTransport(t *testing.T) {
	const bindingID = "00000000-0000-4000-8000-000000000001"
	const environmentID = "00000000-0000-4000-8000-000000000002"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["environment"] != "staging" || body["queue_name"] != "orders" {
				t.Errorf("environment body: %+v %v", body, err)
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/queues/send"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "message", "environment": "staging", "queue_binding_id": bindingID})
		case strings.HasSuffix(r.URL.Path, "/inbox"):
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "message", "environment": "staging", "queue_binding_id": bindingID, "event_id": "event", "target_app": "worker", "status": "pending", "status_url": "/status"})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"binding_id": bindingID, "environment": "staging", "environment_id": environmentID, "consumer_state": "paused", "consumer_state_reason": "environment_unavailable", "consumer_liveness": "not_observed"})
		default:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": bindingID, "environment": "staging", "environment_id": environmentID, "name": "orders", "queue_name": "orders"})
		}
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	binding, err := client.CreateQueueBinding(ctx, "worker", faas.CreateQueueBindingRequest{Environment: "staging", Name: "orders", QueueName: "orders"})
	if err != nil || binding.ID != bindingID || binding.EnvironmentID != environmentID || binding.Environment != "staging" {
		t.Fatalf("binding: %+v %v", binding, err)
	}
	sent, err := client.QueueSend(ctx, "worker", faas.QueueSendRequest{Environment: "staging", QueueName: "orders", Payload: json.RawMessage(`{}`), FlagContext: "flags"})
	if err != nil || sent.QueueBindingID != bindingID || sent.Environment != "staging" {
		t.Fatalf("queue: %+v %v", sent, err)
	}
	inbox, err := client.SendAppMessage(ctx, "worker", faas.SendAppMessageRequest{Environment: "staging", QueueName: "orders", Type: "order.created", Data: json.RawMessage(`{}`)})
	if err != nil || inbox.QueueBindingID != bindingID || inbox.Environment != "staging" {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	status, err := client.GetQueueBindingStatus(ctx, "worker", bindingID)
	if err != nil || status.EnvironmentID != environmentID || status.ConsumerStateReason != "environment_unavailable" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if calls != 4 {
		t.Fatalf("requests: %d", calls)
	}
}

// adr:1352
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueueCanaryClientNeverPublishesWithoutConsent(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/api/bindings/queue-canaries" {
			t.Errorf("path: %s", r.URL.Path)
		}
		var req CreateQueueBindingCanaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.PublishConsent {
			t.Errorf("request: %+v %v", req, err)
		}
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(QueueBindingCanary{ID: "canary", Binding: req.Binding, DeploymentID: req.DeploymentID, Status: "published", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	req := CreateQueueBindingCanaryRequest{Binding: "orders", DeploymentID: "selected", TTLSeconds: 60}
	if _, err := NewPreviewClient(client).CreateQueueBindingCanary(context.Background(), "api", req); err == nil || calls != 0 {
		t.Fatalf("consent: %d %v", calls, err)
	}
	req.PublishConsent = true
	r, err := NewPreviewClient(client).CreateQueueBindingCanary(context.Background(), "api", req)
	if err != nil || r.DeploymentID != "selected" || r.Status != "published" || calls != 1 {
		t.Fatalf("response: %+v %v", r, err)
	}
}

// adr:1352
package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueueCanaryCLIConsentAndExactCompletion(t *testing.T) {
	deployment, id := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	calls := 0
	r := api.QueueBindingCanary{ID: id, Binding: "orders", DeploymentID: deployment, Scope: "production", Status: "published", PublishedAt: now, ExpiresAt: now.Add(time.Minute)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method == http.MethodPost {
			var in api.CreateQueueBindingCanaryRequest
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil || !in.PublishConsent || in.DeploymentID != deployment || in.Binding != "orders" {
				t.Errorf("request: %+v %v", in, err)
			}
		} else {
			r.Status = "succeeded"
			r.DeliveredAt = &now
			r.CompletedAt = &now
		}
		if err := json.NewEncoder(w).Encode(r); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test")
	if _, err := executeQueueBindingCanary(context.Background(), client, "api", "orders", deployment, false, time.Millisecond, ""); err == nil || calls != 0 {
		t.Fatalf("missing consent published: %d %v", calls, err)
	}
	got, err := executeQueueBindingCanary(context.Background(), client, "api", "orders", deployment, true, time.Millisecond, "")
	if err != nil || got.Status != "succeeded" || calls != 2 {
		t.Fatalf("completion: %+v %v calls=%d", got, err, calls)
	}
}

func TestQueueCanaryPolicyDryRunAndMissingConsentHaveNoWrites(t *testing.T) {
	f := newBindingPolicyCLIFixture(t)
	environment := f.policy.Scope
	observed := time.Now()
	f.inventory.Bindings = append(f.inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: environment, QueueEnvironment: &environment, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &observed, QueueCanarySupported: true})
	f.policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceHandlerCompletion}
	client := api.NewClient(f.server.URL, "test")
	planned, code := executeBindingPolicyVerification(context.Background(), client, "api", "", true, true, time.Millisecond, time.Second)
	if code != 0 || !planned.QueuePublishConsentRequired || len(f.requests) != 0 {
		t.Fatalf("dry run: %+v code=%d writes=%d", planned, code, len(f.requests))
	}
	blocked, code := executeBindingPolicyVerification(context.Background(), client, "api", "", false, true, time.Millisecond, time.Second)
	if code == 0 || blocked.ErrorCode != "queue_publish_consent_required" || len(f.requests) != 0 {
		t.Fatalf("consent: %+v code=%d writes=%d", blocked, code, len(f.requests))
	}
}

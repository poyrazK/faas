package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueueReleaseRequirementsValidation(t *testing.T) {
	for _, valid := range []map[string]string{nil, {}, {"orders": QueueEvidenceConsumerReadiness}} {
		if err := ValidateQueueReleaseRequirements(valid); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []map[string]string{{"BAD": QueueEvidenceConsumerReadiness}, {"orders": "connectivity"}, {"": QueueEvidenceConsumerReadiness}} {
		if ValidateQueueReleaseRequirements(invalid) == nil {
			t.Fatal("invalid queue requirement accepted")
		}
	}
	tooMany := map[string]string{}
	for i := 0; i <= QueueReleaseRequirementsMax; i++ {
		tooMany[fmt.Sprintf("queue-%d", i)] = QueueEvidenceConsumerReadiness
	}
	if ValidateQueueReleaseRequirements(tooMany) == nil {
		t.Fatal("unbounded queue policy accepted")
	}
}

func TestQueueReleasePolicyClientUsesExplicitRouteWithoutFallback(t *testing.T) {
	for _, scenario := range []string{"set", "clear", "old server"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			requirements := map[string]string{"orders": QueueEvidenceConsumerReadiness}
			if scenario == "clear" {
				requirements = map[string]string{}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/bindings/release-policy-with-queues" {
					t.Errorf("unsafe route: %s %s", r.Method, r.URL)
				}
				if scenario == "old server" {
					http.NotFound(w, r)
					return
				}
				var request SetBindingReleasePolicyRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.QueueRequirements == nil || request.ObjectStorageRequirements["ASSETS"] != "read_write" {
					t.Errorf("requirements lost: %+v %v", request, err)
				}
				_ = json.NewEncoder(w).Encode(BindingReleasePolicy{QueueRequirements: request.QueueRequirements})
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "test").SetBindingReleasePolicy(context.Background(), "demo", "production", SetBindingReleasePolicyRequest{QueueRequirements: requirements, ObjectStorageRequirements: map[string]string{"ASSETS": "read_write"}})
			if calls != 1 || (err != nil) != (scenario == "old server") {
				t.Fatalf("fallback or qualification: calls=%d error=%v", calls, err)
			}
		})
	}
}

package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckedEnvironmentPromotionUsesDedicatedRoute(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req PromoteProjectEnvironmentRequest
		if r.URL.Path != "/v1/projects/shop/environments/production/promote-with-bindings" || r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil || !req.RequireBindings || req.PromotionToken != "preview" {
			t.Error("lost checked promotion admission")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(ProjectEnvironmentPromotionResponse{PromotionID: "operation", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", Status: "running", BindingsRequired: true})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.PromoteProjectEnvironmentWithBindings(context.Background(), "shop", "production", PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: "preview"})
	if err != nil || calls != 1 || receipt.Status != "running" || !receipt.BindingsRequired {
		t.Fatalf("admission: %+v %v calls=%d", receipt, err, calls)
	}
}

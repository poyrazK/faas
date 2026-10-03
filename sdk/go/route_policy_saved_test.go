package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestSavedRoutePolicyClientWireBinding(t *testing.T) {
	revision := int64(4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if _, exists := request["requirements"]; exists || string(request["saved"]) != "true" || string(request["expected_revision"]) != "4" {
			t.Errorf("saved source wire binding: %v", request)
		}
		switch r.URL.Path {
		case "/v1/apps/demo/route-policy/plan":
			_, _ = w.Write([]byte(`{"version":3,"authority":"server","app":"demo","status":"ready","requirements_revision":4}`))
		case "/v1/apps/demo/route-policy/apply":
			if string(request["confirm"]) != "true" || string(request["expected_plan_sha256"]) != `"reviewed"` || r.Header.Get("Idempotency-Key") != "retry" {
				t.Error("saved apply lost reviewed fingerprint, confirmation or retry key")
			}
			_, _ = w.Write([]byte(`{"receipt":{"id":"receipt","changes":[]},"replayed":true,"gateway_state":"unknown"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := faas.RoutePolicyPlanRequest{Saved: true, ExpectedRevision: &revision, DeploymentID: "00000000-0000-4000-8000-000000000001"}
	plan, err := client.PlanRoutePolicy(context.Background(), "demo", request)
	if err != nil || plan.RequirementsRevision != revision {
		t.Fatalf("saved plan response: %+v %v", plan, err)
	}
	response, err := client.ApplyRoutePolicy(context.Background(), "demo", "retry", faas.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, Confirm: true, ExpectedPlanSHA256: "reviewed"})
	if err != nil || !response.Replayed || response.Receipt.ID != "receipt" {
		t.Fatalf("saved apply response: %+v %v", response, err)
	}
}

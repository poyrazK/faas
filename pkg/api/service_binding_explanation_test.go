package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExplainAppServiceBindingSelectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/frontend/bindings/services/billing/explanation" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("deployment_id") != "dep-id" || query.Get("scope") != "staging" || query.Get("method") != "POST" || query.Get("path") != "/invoices?a=b&c=d" {
			t.Fatalf("selectors lost or not encoded: %v", query)
		}
		_ = json.NewEncoder(w).Encode(ServiceBindingExplanation{Result: "denied", CallerDeploymentID: "dep-id", Scope: "staging"})
	}))
	defer server.Close()
	out, err := NewClient(server.URL, "test").ExplainAppServiceBinding(context.Background(), "frontend", "billing", ExplainServiceBindingOptions{DeploymentID: "dep-id", Scope: "staging", Method: "POST", Path: "/invoices?a=b&c=d"})
	if err != nil || out.Result != "denied" || out.CallerDeploymentID != "dep-id" || out.Scope != "staging" {
		t.Fatalf("explanation=%+v err=%v", out, err)
	}
}

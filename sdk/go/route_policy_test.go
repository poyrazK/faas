package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestCanaryRouteGateClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/route-requirements/gate" {
			t.Error("gate path")
		}
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"app_id":"11111111-1111-4111-8111-111111111111","mode":"report","revision":0}`))
			return
		}
		var request faas.SetCanaryRouteGateRequest
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&request) != nil || request.Mode != "enforce" || request.ExpectedRevision == nil || *request.ExpectedRevision != 0 {
			t.Error("gate CAS binding")
		}
		_, _ = w.Write([]byte(`{"app_id":"11111111-1111-4111-8111-111111111111","mode":"enforce","revision":1,"updated_at":"2026-10-02T08:17:26Z"}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	gate, err := client.GetCanaryRouteGate(context.Background(), "demo")
	if err != nil || gate.Mode != "report" || gate.Revision != 0 {
		t.Fatalf("read: %+v %v", gate, err)
	}
	gate, err = client.SetCanaryRouteGate(context.Background(), "demo", faas.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &gate.Revision})
	if err != nil || gate.Mode != "enforce" || gate.Revision != 1 {
		t.Fatalf("set: %+v %v", gate, err)
	}
}

func TestRoutePolicyClientPreservesRetryKey(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/v1/apps/demo/route-policy/plan":
			_, _ = w.Write([]byte(`{"version":2,"authority":"server","app":"demo","status":"ready"}`))
		case "/v1/apps/demo/route-policy/apply":
			if r.Header.Get("Idempotency-Key") != "reviewed-plan" {
				t.Error("retry key changed")
			}
			var request faas.RoutePolicyApplyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if !request.Confirm || request.Requirements.Version != 1 {
				t.Error("apply request changed")
			}
			_, _ = w.Write([]byte(`{"receipt":{"id":"receipt","changes":[]},"replayed":true,"gateway_state":"unknown"}`))
		case "/v1/apps/demo/route-policy/receipts/receipt":
			_, _ = w.Write([]byte(`{"id":"receipt","changes":[]}`))
		default:
			t.Error("unexpected endpoint")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := faas.RoutePolicyPlanRequest{Requirements: faas.RouteRequirementsConfig{Version: 1}}
	if plan, err := client.PlanRoutePolicy(context.Background(), "demo", request); err != nil || plan.Version != 2 {
		t.Fatalf("plan: %v", err)
	}
	response, err := client.ApplyRoutePolicy(context.Background(), "demo", "reviewed-plan", faas.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, Confirm: true})
	if err != nil || !response.Replayed || response.Receipt.ID != "receipt" {
		t.Fatalf("apply: %v %+v", err, response)
	}
	if receipt, err := client.GetRoutePolicyReceipt(context.Background(), "demo", "receipt"); err != nil || receipt.ID != "receipt" {
		t.Fatalf("receipt: %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestSavedRouteRequirementsClient(t *testing.T) {
	zero, one := int64(0), int64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			var request faas.SaveRouteRequirementsRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 0 || request.Requirements.Version != 2 {
				t.Errorf("save revision not preserved: %+v %v", request, err)
			}
		case "POST":
			var request faas.CheckRouteRequirementsRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 1 || request.DeploymentID != "selected" || r.URL.Path != "/v1/apps/demo/route-requirements/check" {
				t.Errorf("check binding not preserved: %+v %v", request, err)
			}
			_, _ = w.Write([]byte(`{"version":1,"requirements_revision":1,"deployment_id":"selected","report":{"version":2,"status":"violated"}}`))
			return
		}
		if r.URL.Path != "/v1/apps/demo/route-requirements" {
			t.Error("unexpected saved requirements path")
		}
		_, _ = w.Write([]byte(`{"app_id":"app-id","revision":1,"requirements":{"version":2}}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := client.SaveRouteRequirements(context.Background(), "demo", faas.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: faas.RouteRequirementsConfig{Version: 2}}); err != nil || saved.Revision != 1 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if saved, err := client.GetSavedRouteRequirements(context.Background(), "demo"); err != nil || saved.AppID != "app-id" {
		t.Fatalf("get: %+v %v", saved, err)
	}
	if result, err := client.CheckRouteRequirements(context.Background(), "demo", faas.CheckRouteRequirementsRequest{ExpectedRevision: &one, DeploymentID: "selected"}); err != nil || result.Report.Status != "violated" || result.RequirementsRevision != 1 {
		t.Fatalf("check: %+v %v", result, err)
	}
}

func TestAutomaticRouteCheckClient(t *testing.T) {
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if r.URL.Path != "/v1/apps/demo/route-requirements/checks/selected/refresh" || r.ContentLength != 0 {
				t.Error("refresh changed")
			}
			refreshes++
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-requirements/checks/selected" {
			t.Error("lookup changed")
		}
		_, _ = w.Write([]byte(`{"version":1,"app":"demo","app_id":"app-id","deployment_id":"selected","state":"complete","freshness":"stale","stale_reasons":["configuration_changed"],"current_requirements_revision":2,"check":{"version":1,"requirements_revision":1,"report":{"version":2,"status":"satisfied"}}}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshAutomaticRouteCheck(context.Background(), "demo", "selected"); err != nil || refreshes != 1 {
		t.Fatalf("refresh: %v", err)
	}
	result, err := client.GetAutomaticRouteCheck(context.Background(), "demo", "selected")
	if err != nil || result.Freshness != "stale" || result.Check.Report.Status != "satisfied" || result.CurrentRequirementsRevision != 2 {
		t.Fatalf("lookup: %+v %v", result, err)
	}
}

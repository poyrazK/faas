package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func TestRoutesSavedPlanExportApply(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	deployment := "00000000-0000-4000-8000-000000000001"
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"groups":[{"name":"checkout","path_prefix":"/checkout/","methods":["POST"],"require":{"budget":{"explicit":true,"max_ms":500}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	inventory := routerequirements.CoverageInventory{Status: "available", Source: "captured_candidate_contract", Deployment: deployment, SHA256: strings.Repeat("a", 64), RouteCount: 1, Routes: []routerequirements.CapturedRoute{{Method: "POST", Path: "/checkout/{id}"}}}
	context := routerequirements.Context{Host: "my-api.gregale.dev", App: api.AppResponse{ID: "app-id", Slug: "my-api", EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
	artifact := filepath.Join(t.TempDir(), "repair.json")
	var reviewed api.RoutePolicyPlan
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if _, exists := body["requirements"]; exists {
			t.Error("saved workflow sent inline requirements")
		}
		encoded, _ := json.Marshal(body)
		switch r.URL.Path {
		case "/v1/apps/my-api/route-policy/plan":
			var request api.RoutePolicyPlanRequest
			_ = json.Unmarshal(encoded, &request)
			if !request.Saved || request.ExpectedRevision == nil || *request.ExpectedRevision != 4 || request.DeploymentID != deployment {
				t.Errorf("saved planning request: %+v", request)
			}
			var err error
			reviewed, err = routerequirements.BuildServerGroupPlan(config, context, routerequirements.PlanOptions{PlanName: "pro", RequirementsRevision: 4}, inventory)
			if err != nil {
				t.Error(err)
			}
			writeJSONTest(w, reviewed)
		case "/v1/apps/my-api/route-policy/apply":
			var request api.RoutePolicyApplyRequest
			_ = json.Unmarshal(encoded, &request)
			if !request.Saved || request.ExpectedRevision == nil || *request.ExpectedRevision != reviewed.RequirementsRevision || request.ExpectedPlanSHA256 != reviewed.SHA256 || r.Header.Get("Idempotency-Key") != reviewed.SHA256 || request.DeploymentID != deployment {
				t.Errorf("saved apply lost binding: %+v", request)
			}
			writeJSONTest(w, api.RoutePolicyApplyResponse{Receipt: api.RoutePolicyReceipt{ID: "receipt", Verification: reviewed.After}, GatewayState: "unobserved"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	for _, args := range [][]string{
		{"my-api", "--saved"},
		{"my-api", "--saved", "--requirements", "missing.yaml", "--deployment", deployment},
		{"my-api", "--saved", "--deployment", deployment, "--expected-revision", "0"},
		{"my-api", "--saved", "--deployment", deployment, "--expected-revision", "-1"},
		{"my-api", "--requirements", "missing.yaml", "--expected-revision", "4"},
	} {
		if code := cmdRoutesPlan(args); code != 1 || calls != 0 {
			t.Fatalf("invalid flags reached API: %v exit=%d calls=%d", args, code, calls)
		}
	}
	if code := run([]string{"routes", "plan", "my-api", "--saved", "--deployment", deployment, "--expected-revision", "4", "--out", artifact}); code != 0 {
		t.Fatalf("plan: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Saved requirements revision: 4") {
		t.Fatalf("revision missing from review: %s", out.String())
	}
	if plan, err := readRoutePolicyPlan(artifact, "my-api"); err != nil || plan.RequirementsRevision != 4 {
		t.Fatalf("artifact binding: %+v %v", plan, err)
	}
	if code := run([]string{"routes", "apply", "my-api", "--plan", artifact, "--confirm"}); code != 0 || calls != 2 {
		t.Fatalf("apply: %d calls=%d %s", code, calls, out.String())
	}
}

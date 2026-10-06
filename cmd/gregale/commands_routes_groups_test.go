package main

// ADR-446: captured route-group planning, impact and transactional verification.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func TestRoutesGroupPlanExportAndApplyBinding(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	dir := t.TempDir()
	input, artifact := filepath.Join(dir, "routes.yaml"), filepath.Join(dir, "plan.json")
	config := `version: 2
groups:
  - name: checkout
    path_prefix: /checkout/
    methods: [POST]
    require:
      throttle: {key_by: none, max_rps: 1}
      budget: {explicit: true, max_ms: 500}
public:
  - method: GET
    path: /health
    reason: private-local-rationale
`
	if err := os.WriteFile(input, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	deployment := "00000000-0000-4000-8000-000000000001"
	inventory := routerequirements.CoverageInventory{Status: "available", Source: "captured_candidate_contract", Deployment: deployment, SHA256: strings.Repeat("a", 64), RouteCount: 3, Routes: []routerequirements.CapturedRoute{{Method: "POST", Path: "/checkout/{id}/confirm"}, {Method: "POST", Path: "/checkout/reports/list"}, {Method: "GET", Path: "/health"}}}
	context := routerequirements.Context{Host: "my-api.gregale.dev", App: api.AppResponse{ID: "app-id", Slug: "my-api", EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
	calls := 0
	var reviewed api.RoutePolicyPlan
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" {
			t.Errorf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/apps/my-api/route-policy/plan":
			var request api.RoutePolicyPlanRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.DeploymentID != deployment || request.Requirements.Version != 2 || !request.ConsolidateBudgets || request.Requirements.Public[0].Reason != "declared_public_exception" {
				t.Errorf("binding/privacy: %+v", request)
			}
			plan, err := routerequirements.BuildServerGroupPlan(request.Requirements, context, routerequirements.PlanOptions{PlanName: "pro", ThrottleBurst: request.ThrottleBurst, ConsolidateBudgets: request.ConsolidateBudgets}, inventory)
			if err != nil {
				t.Error(err)
				return
			}
			reviewed = plan
			writeJSONTest(w, plan)
		case "/v1/apps/my-api/route-policy/apply":
			var request api.RoutePolicyApplyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.DeploymentID != deployment || request.ExpectedPlanSHA256 != reviewed.SHA256 || r.Header.Get("Idempotency-Key") != reviewed.SHA256 || request.Requirements.Version != 2 || !request.ConsolidateBudgets {
				t.Errorf("apply binding: %+v", request)
			}
			writeJSONTest(w, api.RoutePolicyApplyResponse{Receipt: api.RoutePolicyReceipt{ID: "receipt", Verification: reviewed.After}, GatewayState: "unobserved"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"routes", "plan", "my-api", "--requirements", input, "--throttle-burst", "5", "--out", artifact, "--fail-on-unresolved"}); code != 1 || calls != 0 {
		t.Fatal("missing deployment reached server")
	}
	if code := run([]string{"routes", "plan", "my-api", "--requirements", input, "--deployment", deployment, "--throttle-burst", "5", "--consolidate-budgets", "--out", artifact, "--fail-on-unresolved"}); code != 0 {
		t.Fatalf("plan: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "captured impact: POST /checkout/{id}/confirm") || !strings.Contains(out.String(), "uncaptured and future paths") || !strings.Contains(out.String(), "consolidated budget group: checkout") || !strings.Contains(out.String(), "App rules: 0 -> 3; quota: 100") {
		t.Fatalf("impact missing: %s", out.String())
	}
	body, err := os.ReadFile(artifact)
	if err != nil || strings.Contains(string(body), "private-local-rationale") {
		t.Fatalf("export privacy: %v", err)
	}
	if _, err := readRoutePolicyPlan(artifact, "my-api"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"routes", "apply", "my-api", "--plan", artifact, "--confirm"}); code != 0 || calls != 2 {
		t.Fatalf("apply: %d %s", code, out.String())
	}
}

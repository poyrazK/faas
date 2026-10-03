package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func TestRoutesApplyReviewAndRetryKey(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	config, err := routerequirements.Parse([]byte(routePlanConfig))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := routerequirements.BuildServerPlan(config, routerequirements.Context{Host: "my-api.gregale.dev", App: api.AppResponse{ID: "app-id", Slug: "my-api", ConsumerAuthMode: "required", EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}},
		routerequirements.PlanOptions{PlanName: "pro", ThrottleBurst: 20})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	body, _ := json.Marshal(plan)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/my-api/route-policy/apply" || r.Header.Get("Idempotency-Key") != plan.SHA256 {
			t.Errorf("unexpected apply request")
		}
		var request api.RoutePolicyApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ExpectedPlanSHA256 != plan.SHA256 || !request.Confirm || request.ThrottleBurst != 20 {
			t.Errorf("request=%+v", request)
		}
		writeJSONTest(w, api.RoutePolicyApplyResponse{Receipt: api.RoutePolicyReceipt{ID: "receipt", PlanSHA256: plan.SHA256}, GatewayState: "converging"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	for _, args := range [][]string{
		{"routes", "apply", "my-api", "--plan", path},
		{"routes", "apply", "other-api", "--plan", path, "--confirm"},
	} {
		if code := run(args); code != 1 {
			t.Fatal("unconfirmed or foreign plan applied")
		}
	}
	if calls != 0 {
		t.Fatal("invalid apply reached network")
	}
	if code := run([]string{"routes", "apply", "my-api", "--plan", path, "--confirm", "--json"}); code != 0 {
		t.Fatalf("apply failed: %s", out.String())
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	plan.Changes[0].Reason = "edited patch"
	body, _ = json.Marshal(plan)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "apply", "my-api", "--plan", path, "--confirm"}); code != 1 || calls != 1 {
		t.Fatal("edited artifact reached apply")
	}
}

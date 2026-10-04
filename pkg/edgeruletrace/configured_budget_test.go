package edgeruletrace

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-436: requirements compare the configured baseline, not a caller's override.
func TestConfiguredBudgetUsesSharedResolutionWithoutHeaderOverride(t *testing.T) {
	input := Input{AppRequestBudgetLoaded: true, RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000,
		Headers: http.Header{api.RequestBudgetDefaultOverrideHeader: []string{"15000"}}}
	rule := &api.EdgeRuleResponse{Kind: "budget", Action: json.RawMessage(`{"budget":{"budget_ms":2000}}`)}
	if policy := ConfiguredBudget(input, rule); policy.BudgetMS != 2000 || policy.Source != "rule" || policy.OverrideStatus != "not_present" {
		t.Fatalf("request override became configured baseline: %+v", policy)
	}
	input.RequestBudgetMaxMS = 1000
	if policy := ConfiguredBudget(input, rule); policy.BudgetMS != 1000 || policy.Source != "ceiling_clamp" {
		t.Fatalf("plan ceiling ignored: %+v", policy)
	}
	input.AppRequestBudgetLoaded = false
	if policy := ConfiguredBudget(input, rule); policy.Source != "unavailable" {
		t.Fatal("missing envelope became a resolved budget")
	}
}

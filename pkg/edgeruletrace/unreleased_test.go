package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// adr: 20261023113721 — a challenge rule whose account has not been released
// challenge rules is inert at the gateway, so the simulator skips it and says
// why instead of predicting a challenge.
func TestSimulateUnreleasedChallengeRule(t *testing.T) {
	rule := api.EdgeRuleResponse{
		ID: "rule-1", MatchHost: "example.com", MatchPath: "/login", Priority: 10, Enabled: true, Kind: "challenge",
		Action:             json.RawMessage(`{"kind":"challenge","challenge":{"difficulty":12,"pass_ttl_seconds":600}}`),
		Mode:               api.EdgeRuleModeEnforce,
		UnreleasedFeatures: []string{api.EdgeFeatureChallenge},
	}
	fetch := http.Header{}
	fetch.Set("User-Agent", "curl/8.4")
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/login", Method: http.MethodPost, AppMaintenanceLoaded: true, Headers: fetch}
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.StoppedAt == "challenge" || result.Simulation.ProblemCode == api.CodeChallengeRequired {
		t.Fatalf("simulated an unreleased challenge rule: %+v", result.Simulation)
	}
	if len(result.Rules) != 1 || result.Rules[0].Status != "skipped" || result.Rules[0].Reason == "" {
		t.Fatalf("rule rows = %+v, want the rule skipped with a reason", result.Rules)
	}
	rule.UnreleasedFeatures = nil
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
	if err != nil || result.Simulation.StoppedAt != "challenge" {
		t.Fatalf("released rule: %+v, %v", result.Simulation, err)
	}
}

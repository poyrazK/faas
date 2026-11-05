package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// Production-us rc.294: an enforced rate limit on GET /healthz traced as a
// matching throttle candidate, but the gateway answers the health path itself
// before any edge rule and the rule never returned 429 (Hunt 14 H14-49).
func TestSimulateStopsAtGatewayEdgeAnswers(t *testing.T) {
	rule := func(path string) api.EdgeRuleResponse {
		return api.EdgeRuleResponse{
			ID: "rl", Enabled: true, Kind: "throttle", MatchHost: "h14-py.gregale.dev", MatchPath: path, Mode: "enforce",
			Action: json.RawMessage(`{"throttle":{"requests_per_second":0.0833,"burst":5,"key_by":"ip"}}`),
		}
	}
	for _, tc := range []struct {
		name, method, path, healthPath string
		wakes, loaded, wantAnswer      bool
	}{
		{"GET on the default health path", http.MethodGet, "/healthz", "", false, true, true},
		{"HEAD on a custom health path", http.MethodHead, "/up", "up", false, true, true},
		{"favicon", http.MethodGet, "/favicon.ico", "", false, true, true},
		{"robots", http.MethodGet, "/robots.txt", "", false, true, true},
		{"POST reaches the rules", http.MethodPost, "/healthz", "", false, true, false},
		{"health_path_wakes reaches the rules", http.MethodGet, "/healthz", "", true, true, false},
		{"another path reaches the rules", http.MethodGet, "/", "", false, true, false},
		{"unknown app settings keep the old trace", http.MethodGet, "/healthz", "", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := edgeruletrace.Input{
				App: "h14-py", Host: "h14-py.gregale.dev", Path: tc.path, Method: tc.method, AppMaintenanceLoaded: true,
				EdgeAnswersLoaded: tc.loaded, AppHealthPath: tc.healthPath, AppHealthPathWakes: tc.wakes,
			}
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule(tc.path)})
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			answered := result.Simulation.StoppedAt == "edge_answer" && result.Simulation.Outcome == "edge_answer" && result.Simulation.Status == "complete"
			if answered != tc.wantAnswer {
				t.Fatalf("edge answer = %v, want %v: %#v", answered, tc.wantAnswer, result.Simulation)
			}
		})
	}
	if got := edgeruletrace.EdgeAnswerHealthPath("", "/ready"); got != "/ready" {
		t.Fatalf("EdgeAnswerHealthPath fallback = %q, want /ready", got)
	}
}

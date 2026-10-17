package api

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestColdStartEnrollmentRequiresExactAcknowledgement(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "different_budget"} {
		t.Run(mode, func(t *testing.T) {
			plan := ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []ScenarioSimulationResponse{{Status: 200}}}}}
			req := RegisterScenarioTestRequest{ColdStart: &rehearsalcold.Spec{Version: 1, WithinMS: 1000}, CommandVersion: 1, Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "isolated-preview"}}, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &plan}
			_, cold, _ := ScenarioColdStartEnrollment(req)
			_, sim, _ := NormalizeScenarioSimulationPlan(plan, []string{"primary"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ColdStart == nil || got.ColdStart.WithinMS != 1000 {
					t.Error("cold-start intent was not transmitted")
				}
				ack := RegisterScenarioTestResponse{ColdStartSHA256: cold, CommandVersion: 1, RecorderVersion: 1, SimulationSHA256: sim, NetworkMode: "contained", WebhookMode: "capture", PolicyVersion: 1, ActionEvidenceVersion: 2}
				if mode == "missing" {
					ack.ColdStartSHA256 = ""
				}
				if mode == "different_budget" {
					ack.ColdStartSHA256 = strings.Repeat("f", 64)
				}
				_ = json.NewEncoder(w).Encode(ack)
			}))
			defer server.Close()
			err := NewClient(server.URL, "key").RegisterScenarioTest(t.Context(), strings.Repeat("a", 32), req)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("acknowledgement: %v", err)
			}
		})
	}
}

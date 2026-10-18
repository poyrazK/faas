package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestScenarioManagedWorkEnrollmentRequiresExactAcknowledgement(t *testing.T) {
	for _, mode := range []string{"exact", "missing", "different"} {
		t.Run(mode, func(t *testing.T) {
			plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true, Objects: "read"}}}
			simulation := ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "worker", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []ScenarioSimulationResponse{{Status: 200}}}}}
			req := RegisterScenarioTestRequest{CommandVersion: 1, ManagedWork: &plan, Simulation: &simulation, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "preview-api"}, {Workload: "worker", AppSlug: "preview-worker"}}}
			_, digest, e := ScenarioManagedWorkEnrollment(req)
			if e != nil {
				t.Fatal(e)
			}
			_, simulationDigest, _ := NormalizeScenarioSimulationPlan(simulation, []string{"primary", "worker"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got RegisterScenarioTestRequest
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.ManagedWork == nil {
					t.Error("participant plan missing on wire")
				}
				ack := RegisterScenarioTestResponse{CommandVersion: 1, RecorderVersion: 1, ManagedWorkSHA256: digest, SimulationSHA256: simulationDigest, NetworkMode: "contained", WebhookMode: "capture", PolicyVersion: 1, ActionEvidenceVersion: 2}
				if mode == "missing" {
					ack.ManagedWorkSHA256 = ""
				}
				if mode == "different" {
					ack.ManagedWorkSHA256 = strings.Repeat("f", 64)
				}
				_ = json.NewEncoder(w).Encode(ack)
			}))
			defer server.Close()
			if err := NewClient(server.URL, "fixture-token").RegisterScenarioTest(t.Context(), strings.Repeat("b", 32), req); (err == nil) != (mode == "exact") {
				t.Fatalf("acknowledgement %s: %v", mode, err)
			}
		})
	}
}

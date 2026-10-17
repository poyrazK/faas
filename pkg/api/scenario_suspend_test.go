package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func TestSuspendResumeEnrollmentRequiresExactAcknowledgement(t *testing.T) {
	for _, mode := range []string{"complete", "old_server", "wrong_plan"} {
		t.Run(mode, func(t *testing.T) {
			plan := ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []ScenarioSimulationResponse{{Status: 200}}}}}
			req := RegisterScenarioTestRequest{SuspendResume: &rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseMS: 1, WithinMS: 1000}, CommandVersion: 1, Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "isolated-preview"}}, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &plan}
			_, d, _ := ScenarioSuspendResumeEnrollment(req)
			_, sim, _ := NormalizeScenarioSimulationPlan(plan, []string{"primary"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.SuspendResume == nil || got.SuspendResume.PauseMS != 1 {
					t.Error("request lost lifecycle plan")
				}
				ack := RegisterScenarioTestResponse{SuspendResumeSHA256: d, CommandVersion: 1, RecorderVersion: 1, SimulationSHA256: sim, NetworkMode: "contained", WebhookMode: "capture", PolicyVersion: 1, ActionEvidenceVersion: 2}
				if mode == "old_server" {
					ack.SuspendResumeSHA256 = ""
				}
				if mode == "wrong_plan" {
					ack.SuspendResumeSHA256 = strings.Repeat("f", 64)
				}
				_ = json.NewEncoder(w).Encode(ack)
			}))
			defer server.Close()
			client := NewClient(server.URL, "token")
			err := client.RegisterScenarioTest(t.Context(), strings.Repeat("a", 32), req)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("acknowledgement: %v", err)
			}
		})
	}
}

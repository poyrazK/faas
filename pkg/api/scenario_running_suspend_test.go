// adr: 1359
package api

import (
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"testing"
)

func TestRunningSuspendRequiresOneWorkerAndSuccessfulCheckpoint(t *testing.T) {
	for _, mode := range []string{"valid", "no_plan", "wrong_worker", "two_workers", "missing_fixture", "fault", "delay", "multiple_responses", "wrong_adapter", "termination", "missing_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			plan := &rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}}
			simulation := &ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []ScenarioSimulationResponse{{Status: 200}}}}}
			spec := rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 1, WithinMS: 1000}
			switch mode {
			case "no_plan":
				plan = nil
			case "wrong_worker":
				spec.Workloads = []string{"primary"}
			case "two_workers":
				plan.Participants[0].Async = true
			case "missing_fixture":
				simulation.Fixtures = nil
			case "fault":
				simulation.Fixtures[0].Responses[0].Fault = "disconnect"
			case "delay":
				simulation.Fixtures[0].Responses[0].DelayMS = 1
			case "multiple_responses":
				simulation.Fixtures[0].Responses = append(simulation.Fixtures[0].Responses, ScenarioSimulationResponse{Status: 200})
			case "wrong_adapter":
				simulation.Fixtures[0].Adapter = "resend"
			case "termination":
				plan.WorkerTermination = &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}
			case "missing_checkpoint":
				spec.WorkerCheckpoint = ""
			}
			req := RegisterScenarioTestRequest{CommandVersion: 1, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: simulation, ManagedWork: plan, SuspendResume: &spec, Members: []ScenarioTestWorkload{{Workload: "primary"}, {Workload: "worker"}}}
			_, _, err := ScenarioSuspendResumeEnrollment(req)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

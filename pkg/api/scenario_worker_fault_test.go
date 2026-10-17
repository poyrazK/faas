package api

import (
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"testing"
)

func TestWorkerTerminationEnrollmentRequiresSupportedCheckpoint(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong_worker", "resend", "get", "delay", "fault", "sequence"} {
		t.Run(mode, func(t *testing.T) {
			plan := rehearsalwork.Plan{WorkerTermination: &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}}
			f := ScenarioSimulationFixture{Workload: "worker", Name: "after-commit", Adapter: "http", Method: "POST", Responses: []ScenarioSimulationResponse{{Status: 200}}}
			switch mode {
			case "missing":
				f.Name = "other"
			case "wrong_worker":
				f.Workload = "primary"
			case "resend":
				f.Adapter = "resend"
			case "get":
				f.Method = "GET"
			case "delay":
				f.Responses[0].DelayMS = 1
			case "fault":
				f.Responses[0].Fault = "disconnect"
			case "sequence":
				f.Responses = append(f.Responses, f.Responses[0])
			}
			err := ValidateScenarioWorkerTermination(plan, &ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{f}})
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

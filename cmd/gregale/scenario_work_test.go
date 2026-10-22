package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestWorkerTerminationRecipeRequiresMatchingWorkerCheckpoint(t *testing.T) {
	for _, mode := range []string{"valid", "missing_checkpoint", "wrong_worker", "delayed_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			s := main.TestScenario{Project: "primary", CommandExecution: &main.TestCommandExecutionSpec{Version: 1, Runtime: "contained"}, Actions: &main.TestActionSpec{Simulation: &api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}}, Services: map[string]main.TestService{"worker": {Source: "worker", AsyncRoutes: []main.TestAsyncRoute{{Path: "/import", Methods: []string{"POST"}}}}}, ManagedWork: &rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}, WorkerTermination: &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}}}
			switch mode {
			case "missing_checkpoint":
				s.Actions.Simulation.Fixtures = nil
			case "wrong_worker":
				s.ManagedWork.WorkerTermination.Workload = "primary"
			case "delayed_checkpoint":
				s.Actions.Simulation.Fixtures[0].Responses[0].DelayMS = 1
			}
			if err := main.ValidateTestManagedWork(s); (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "valid" {
				digest := main.TestManagedWorkDigest(s)
				s.ManagedWork.WorkerTermination = nil
				if main.TestManagedWorkDigest(s) == digest {
					t.Fatal("termination changed no recipe identity")
				}
			}
		})
	}
}

func TestManagedWorkRecipeRequiresExplicitResourcesAndWorkerRoutes(t *testing.T) {
	for _, mode := range []string{"valid", "missing_route", "unknown_workload", "missing_fixture", "reserved_secret", "wrong_primary"} {
		t.Run(mode, func(t *testing.T) {
			s := main.TestScenario{Project: "primary", CommandExecution: &main.TestCommandExecutionSpec{Version: 1, Runtime: "contained"}, Actions: &main.TestActionSpec{Simulation: &api.ScenarioSimulationPlan{Version: 1}}, ObjectsFixture: &main.TestObjectSpec{Version: 1, Bucket: "imports"}, Services: map[string]main.TestService{"worker": {Source: "worker", AsyncRoutes: []main.TestAsyncRoute{{Path: "/import", Methods: []string{"POST"}}}}}, ManagedWork: &rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary", Objects: "read_write"}, {Workload: "worker", Async: true, Objects: "read_write"}}}}
			v := s.Services["worker"]
			switch mode {
			case "missing_route":
				v.AsyncRoutes = nil
			case "unknown_workload":
				s.ManagedWork.Participants[1].Workload = "other"
			case "missing_fixture":
				s.ObjectsFixture = nil
			case "reserved_secret":
				v.Secrets = map[string]string{"aws_endpoint_url_s3": "https://live.example.invalid"}
			case "wrong_primary":
				s.ManagedWork.Primary = "worker"
			}
			s.Services["worker"] = v
			if err := main.ValidateTestManagedWork(s); (err == nil) != (mode == "valid") {
				t.Fatalf("validation %s: %v", mode, err)
			}
			if mode == "valid" {
				old := main.TestManagedWorkDigest(s)
				s.ManagedWork.Participants[1].Objects = "read"
				if old == main.TestManagedWorkDigest(s) || len(old) != 64 || strings.Trim(old, "0123456789abcdef") != "" {
					t.Fatal("permissions were not pinned")
				}
			}
		})
	}
}

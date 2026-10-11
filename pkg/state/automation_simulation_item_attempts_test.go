package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func itemAttemptRequest() api.SimulateAutomationRequest {
	return api.SimulateAutomationRequest{Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":[1,2]}`), MockItemAttempts: map[string]map[string][]api.AutomationSimulationMockAttempt{"batch": {"0": {{Outcome: "failure", Error: "private-mock-error"}, {Outcome: "success", Output: json.RawMessage(`false`)}}, "1": {{Outcome: "success", Output: json.RawMessage(`null`)}}}}, MockOutputs: map[string]json.RawMessage{"finish": json.RawMessage(`true`)}}
}

func TestAutomationSimulationItemAttemptOutcomes(t *testing.T) {
	for _, mode := range []string{"retry_success", "pending_retry", "stop_failure", "continue_failure", "exhaustion", "timeout_retry", "timeout_exhaustion", "guard_skip"} {
		t.Run(mode, func(t *testing.T) {
			request := itemAttemptRequest()
			request.Definition.Steps[0].ForEach.Action.Retry = &api.WorkflowRetrySpec{MaxAttempts: 2}
			switch mode {
			case "pending_retry":
				request.MockItemAttempts["batch"]["0"] = request.MockItemAttempts["batch"]["0"][:1]
			case "stop_failure", "continue_failure":
				status := 400
				request.MockItemAttempts["batch"]["0"] = []api.AutomationSimulationMockAttempt{{Outcome: "failure", HTTPStatus: &status}}
				if mode == "continue_failure" {
					request.Definition.Steps[0].ForEach.OnItemFailure = "continue"
				}
			case "exhaustion":
				request.MockItemAttempts["batch"]["0"][1] = api.AutomationSimulationMockAttempt{Outcome: "failure", Error: "private-mock-error"}
			case "timeout_retry", "timeout_exhaustion":
				request.Definition.Steps[0].ForEach.Action.Timeout = time.Minute
				request.MockItemAttempts["batch"]["0"][0] = api.AutomationSimulationMockAttempt{Outcome: "timeout"}
				if mode == "timeout_exhaustion" {
					request.MockItemAttempts["batch"]["0"][1] = api.AutomationSimulationMockAttempt{Outcome: "timeout"}
				}
			case "guard_skip":
				request.Definition.Steps[0].ForEach.Action.When = &api.WorkflowGuardSpec{Ref: "input.item", Op: "eq", Value: json.RawMessage(`2`)}
			}
			response := simulateTest(t, request)
			parent := simulationStep(t, response, "batch")
			first := simulationStep(t, response, api.WorkflowForEachItemName("batch", 0))
			second := simulationStep(t, response, api.WorkflowForEachItemName("batch", 1))
			if response.Complete != (mode != "pending_retry") {
				t.Fatalf("complete: %+v", response)
			}
			switch mode {
			case "retry_success", "timeout_retry":
				if parent.State != "resolved" || string(parent.Output) != `[false,null]` || len(first.Attempts) != 2 || second.State != "mocked" {
					t.Fatalf("success: %+v", response)
				}
			case "guard_skip":
				if first.State != "skipped" || string(parent.Output) != `[null,null]` || len(response.Warnings) != 1 {
					t.Fatalf("guard: %+v", response)
				}
			case "pending_retry":
				if first.State != "would_retry" || second.State != "blocked" || parent.State != "expanded" {
					t.Fatalf("pending: %+v", response)
				}
			case "continue_failure":
				if parent.State != "failed" || string(parent.Output) != `[null,null]` || second.State != "mocked" {
					t.Fatalf("continue: %+v", response)
				}
			default:
				state := "dead"
				if mode == "stop_failure" {
					state = "failed"
				}
				if parent.State != state || string(parent.Output) != `[]` || second.State != "skipped" {
					t.Fatalf("stop: %+v", response)
				}
			}
			if strings.HasPrefix(mode, "timeout") && first.Attempts[0].Outcome != "timeout" {
				t.Fatal("timeout summary lost")
			}
			raw, _ := json.Marshal(response)
			if strings.Contains(string(raw), "private-mock-error") {
				t.Fatal("trace leaked mock error")
			}
		})
	}
}

func TestAutomationSimulationItemAttemptValidation(t *testing.T) {
	for _, mode := range []string{"negative", "noncanonical", "too_large", "out_of_range", "empty", "unknown_loop", "duplicate_outputs", "invalid_outcome", "after_success", "after_failure", "timeout_unconfigured", "invalid_json", "too_many_attempts"} {
		t.Run(mode, func(t *testing.T) {
			request := itemAttemptRequest()
			switch mode {
			case "negative", "noncanonical", "too_large", "out_of_range":
				key := map[string]string{"negative": "-1", "noncanonical": "01", "too_large": "128", "out_of_range": "2"}[mode]
				request.MockItemAttempts["batch"][key] = request.MockItemAttempts["batch"]["0"]
			case "empty":
				request.MockItemAttempts["batch"]["0"] = nil
			case "unknown_loop":
				request.MockItemAttempts["absent"] = request.MockItemAttempts["batch"]
			case "duplicate_outputs":
				request.MockItemOutputs = map[string][]json.RawMessage{"batch": {json.RawMessage(`true`)}}
			case "invalid_outcome":
				request.MockItemAttempts["batch"]["0"][0].Outcome = "other"
			case "after_success":
				request.MockItemAttempts["batch"]["0"][0] = api.AutomationSimulationMockAttempt{Outcome: "success", Output: json.RawMessage(`true`)}
			case "after_failure":
				status := 400
				request.MockItemAttempts["batch"]["0"][0] = api.AutomationSimulationMockAttempt{Outcome: "failure", HTTPStatus: &status}
			case "timeout_unconfigured":
				request.MockItemAttempts["batch"]["0"][0] = api.AutomationSimulationMockAttempt{Outcome: "timeout"}
			case "invalid_json":
				request.MockItemAttempts["batch"]["0"][1].Output = json.RawMessage(`invalid`)
			case "too_many_attempts":
				request.MockItemAttempts["batch"]["0"] = make([]api.AutomationSimulationMockAttempt, 26)
			}
			_, err := SimulateAutomation(context.Background(), request, api.PlanHobby)
			var limit *AutomationSimulationLimitError
			if !errors.Is(err, ErrAutomationSimulationInvalid) && !errors.As(err, &limit) {
				t.Fatalf("accepted invalid mocks: %v", err)
			}
		})
	}
}

func TestAutomationSimulationUnsafeItemDoesNotRetry(t *testing.T) {
	request := itemAttemptRequest()
	request.Definition.Steps[0].ForEach.Action = api.WorkflowForEachActionSpec{Outbound: &api.WorkflowOutboundSpec{IntegrationID: "00000000-0000-4000-8000-000000000001", Method: "POST", Path: "/send"}}
	// An unsafe outbound failure is terminal, so a following success must be rejected.
	_, err := SimulateAutomation(context.Background(), request, api.PlanHobby)
	if !errors.Is(err, ErrAutomationSimulationInvalid) {
		t.Fatalf("unsafe item retried: %v", err)
	}
}

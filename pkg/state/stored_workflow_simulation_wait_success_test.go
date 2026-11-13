package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoredWorkflowSimulationSuccessfulWaits(t *testing.T) {
	for _, callback := range []bool{false, true} {
		for _, payload := range []string{`{"approved_by":"reviewer-42"}`, `null`, `[1,true]`, `9007199254740993`} {
			wait := api.WorkflowStepSpec{Name: "approval", Timeout: time.Hour, OnTimeout: "expire", WaitForEvent: "order.approved"}
			reason := "event_received_mocked"
			if callback {
				wait.WaitForEvent, wait.WaitForCallback, reason = "", true, "callback_received_mocked"
			}
			request := api.SimulateStoredWorkflowRequest{
				Definition:   api.WorkflowSpec{Name: "approve", Steps: []api.WorkflowStepSpec{wait, {Name: "fulfill", Run: "fulfill", DependsOn: []string{"approval"}, Input: json.RawMessage(`{"payload":"{{steps.approval.output}}"}`)}, {Name: "expire", Run: "expire", DependsOn: []string{"approval"}}}},
				MockAttempts: map[string][]api.StoredWorkflowSimulationMockAttempt{"approval": {{Outcome: "success", Output: json.RawMessage(payload)}}},
				MockOutputs:  map[string]json.RawMessage{"fulfill": json.RawMessage(`true`)},
			}
			result := state.SimulateTest(t, request)
			approval := state.SimulationStep(t, result, "approval")
			fulfill := state.SimulationStep(t, result, "fulfill")
			if !result.Complete || approval.State != "mocked" || approval.Reason != reason || string(approval.Output) != payload || len(approval.Attempts) != 1 || approval.Attempts[0].Outcome != "success" || string(fulfill.Input) != `{"payload":`+payload+`}` || state.SimulationStep(t, result, "expire").State != "skipped" {
				t.Fatalf("callback=%v payload=%s: %+v", callback, payload, result)
			}
		}
	}
}

func TestStoredWorkflowSimulationRejectsIncompatibleWaitSuccess(t *testing.T) {
	wait := api.WorkflowStepSpec{Name: "approval", WaitForEvent: "approved", Timeout: time.Hour}
	for _, attempts := range [][]api.StoredWorkflowSimulationMockAttempt{
		{{Outcome: "success", Output: json.RawMessage(`{"timeout":true}`)}},
		{{Outcome: "success"}},
		{{Outcome: "failure", Error: "failed"}},
		{{Outcome: "success", Output: json.RawMessage(`null`)}, {Outcome: "success", Output: json.RawMessage(`null`)}},
	} {
		request := api.SimulateStoredWorkflowRequest{Definition: api.WorkflowSpec{Name: "approve", Steps: []api.WorkflowStepSpec{wait}}, MockAttempts: map[string][]api.StoredWorkflowSimulationMockAttempt{"approval": attempts}}
		if _, err := state.SimulateStoredWorkflow(context.Background(), request, api.PlanHobby); !errors.Is(err, state.ErrStoredWorkflowSimulationInvalid) {
			t.Fatalf("accepted incompatible wait outcomes: %v", err)
		}
	}
}

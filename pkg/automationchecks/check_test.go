package automationchecks

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAssertionsAndCoverage(t *testing.T) {
	def := api.WorkflowSpec{Name: "guard", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send", When: &api.WorkflowGuardSpec{Ref: "input.ok", Op: "exists", Value: json.RawMessage(`true`)}}}}
	expectations := []api.AutomationCheckExpectation{{Step: "send", State: "mocked", Output: json.RawMessage(`9007199254740993`)}}
	if err := ValidateExpectations(def, expectations); err != nil {
		t.Fatal(err)
	}
	yes := true
	r := api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{{StepName: "send", State: "mocked", WhenMatched: &yes, Output: json.RawMessage(`9007199254740993.0`)}}}
	if !Passes(expectations, r) {
		t.Fatal("exact numeric assertion failed")
	}
	r.Trace[0].Output = json.RawMessage(`9007199254740992`)
	if Passes(expectations, r) {
		t.Fatal("rounded distinct values matched")
	}
	r.Trace[0].Output = json.RawMessage(`9007199254740993`)
	coverage := Coverage{}
	coverage.Observe(r)
	if n, err := RemainingCoverage(def, coverage, nil); err != nil || n != 1 {
		t.Fatalf("missing skip: %d %v", n, err)
	}
	excluded := []api.AutomationCheckExclusion{{Step: "send", Code: "guard_skip_missing", Reason: "Documented constraint"}}
	if n, err := RemainingCoverage(def, coverage, excluded); err != nil || n != 0 {
		t.Fatalf("exclusion: %d %v", n, err)
	}
	excluded[0].Step = "unknown"
	if _, err := RemainingCoverage(def, coverage, excluded); err == nil {
		t.Fatal("invalid exclusion accepted")
	}
	r.Complete = false
	if Passes(expectations, r) {
		t.Fatal("incomplete trace passed")
	}
	r.Complete = true
	r.Trace = append(r.Trace, r.Trace[0])
	if Passes(expectations, r) {
		t.Fatal("duplicate trace passed")
	}
}

func TestItemAssertionsKeepRootAndItemOutcomesDistinct(t *testing.T) {
	def := api.WorkflowSpec{Name: "items", Steps: []api.WorkflowStepSpec{
		{Name: "loop", ForEach: &api.WorkflowForEachSpec{Action: api.WorkflowForEachActionSpec{Path: "/item"}}},
		{Name: "loop/action", Path: "/root"},
	}}
	index, status, count := 0, 500, 2
	attempts := []api.AutomationCheckAttempt{{Outcome: "failure", HTTPStatus: &status}, {Outcome: "success"}}
	expectations := []api.AutomationCheckExpectation{
		{Step: "loop/action", State: "mocked", Output: json.RawMessage(`null`)},
		{Step: "loop/action", Loop: "loop", ItemIndex: &index, State: "mocked", Output: json.RawMessage(`1`), AttemptCount: &count, Attempts: &attempts},
	}
	if err := ValidateExpectations(def, expectations); err != nil {
		t.Fatal(err)
	}
	response := api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{
		{StepName: "loop/action", State: "mocked", Output: json.RawMessage(`null`)},
		{StepName: "loop[0]", ParentStep: "loop", ItemIndex: &index, State: "mocked", Output: json.RawMessage(`1.0`), Attempts: []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: "failure", HTTPStatus: &status}, {Attempt: 2, Outcome: "success"}}},
	}}
	if !Passes(expectations, response) {
		t.Fatal("root and loop item assertions did not pass")
	}
	response.Trace[1].Attempts[0].Attempt = 2
	if Passes(expectations, response) {
		t.Fatal("unordered attempts passed")
	}
	response.Trace[1].Attempts[0].Attempt = 1
	response.Trace[0].Output = nil
	if Passes(expectations, response) {
		t.Fatal("missing output matched explicit null")
	}
	response.Trace[0].Output = json.RawMessage(`null`)
	response.Trace = append(response.Trace, response.Trace[1])
	if Passes(expectations, response) {
		t.Fatal("duplicate item outcomes passed")
	}
	expectations[1].Loop = "missing"
	if ValidateExpectations(def, expectations) == nil {
		t.Fatal("unknown loop expectation accepted")
	}
}

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"
)

// AutomationTestInputs contains explicitly saved samples, never a definition or
// execution authority. Every run binds these inputs to the current saved draft.
type AutomationTestInputs struct {
	Input            json.RawMessage                                         `json:"input,omitempty"`
	MockOutputs      map[string]json.RawMessage                              `json:"mock_outputs,omitempty"`
	MockItemOutputs  map[string][]json.RawMessage                            `json:"mock_item_outputs,omitempty"`
	MockItemAttempts map[string]map[string][]AutomationSimulationMockAttempt `json:"mock_item_attempts,omitempty"`
	MockAttempts     map[string][]AutomationSimulationMockAttempt            `json:"mock_attempts,omitempty"`
}

func TestInputsFromSimulation(sim SimulateAutomationRequest) AutomationTestInputs {
	return AutomationTestInputs{Input: sim.Input, MockOutputs: sim.MockOutputs, MockItemOutputs: sim.MockItemOutputs,
		MockItemAttempts: sim.MockItemAttempts, MockAttempts: sim.MockAttempts}
}

func (inputs AutomationTestInputs) Simulation(def WorkflowSpec) SimulateAutomationRequest {
	return SimulateAutomationRequest{Definition: def, Input: inputs.Input, MockOutputs: inputs.MockOutputs,
		MockItemOutputs: inputs.MockItemOutputs, MockItemAttempts: inputs.MockItemAttempts, MockAttempts: inputs.MockAttempts}
}

type AutomationTestCase struct {
	Name         string                       `json:"name"`
	Simulation   AutomationTestInputs         `json:"simulation"`
	Expectations []AutomationCheckExpectation `json:"expectations"`
	Live         *AutomationLiveTest          `json:"live,omitempty"`
}
type AutomationTestSuite struct {
	Sample AutomationTestInputs `json:"sample"`
	Cases  []AutomationTestCase `json:"cases"`
}
type SaveAutomationTestSuiteRequest struct {
	ExpectedVersion int64                `json:"expected_version"`
	Suite           *AutomationTestSuite `json:"suite"` // null clears saved data and results
}
type RunAutomationTestsRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

// AutomationTestReport retains safe case/step summaries, without mock payloads,
// outputs, errors or a publication receipt.
type AutomationTestReport struct {
	CheckedVersion int64                      `json:"checked_version"`
	CheckedAt      time.Time                  `json:"checked_at"`
	InputsHash     string                     `json:"inputs_hash"`
	Passed         bool                       `json:"passed"`
	Cases          []AutomationTestCaseResult `json:"cases"`
}
type AutomationTestCaseResult struct {
	Name           string                     `json:"name"`
	Passed         bool                       `json:"passed"`
	Issue          string                     `json:"issue,omitempty"`
	StepsTruncated bool                       `json:"steps_truncated,omitempty"`
	Steps          []AutomationTestStepResult `json:"steps"`
}
type AutomationTestStepResult struct {
	Step       string   `json:"step"`
	Loop       string   `json:"loop,omitempty"`
	ItemIndex  *int     `json:"item_index,omitempty"`
	State      string   `json:"state,omitempty"`
	Passed     bool     `json:"passed"`
	Mismatches []string `json:"mismatches,omitempty"`
}

// AutomationTestInputsHash tolerates JSONB object-key ordering without losing
// numeric precision. It deliberately preserves array order and number literals.
func AutomationTestInputsHash(def WorkflowSpec, suite AutomationTestSuite) (string, error) {
	raw, err := json.Marshal(struct {
		Definition WorkflowSpec
		Suite      AutomationTestSuite
	}{def, suite})
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (c *Client) SaveAutomationTestSuite(ctx context.Context, slug, name string, body SaveAutomationTestSuiteRequest) (AutomationResponse, error) {
	var out AutomationResponse
	err := c.do(ctx, "PUT", automationPath(slug)+"/"+url.PathEscape(name)+"/tests", body, &out)
	return out, err
}
func (c *Client) RunAutomationTests(ctx context.Context, slug, name string, body RunAutomationTestsRequest) (AutomationTestReport, error) {
	var out AutomationTestReport
	err := c.do(ctx, "POST", automationPath(slug)+"/"+url.PathEscape(name)+"/tests/run", body, &out)
	return out, err
}

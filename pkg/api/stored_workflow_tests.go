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

// StoredWorkflowTestInputs contains explicitly saved samples, never a definition or
// execution authority. Every run binds these inputs to the current saved draft.
type StoredWorkflowTestInputs struct {
	Input            json.RawMessage                                             `json:"input,omitempty"`
	MockOutputs      map[string]json.RawMessage                                  `json:"mock_outputs,omitempty"`
	MockItemOutputs  map[string][]json.RawMessage                                `json:"mock_item_outputs,omitempty"`
	MockItemAttempts map[string]map[string][]StoredWorkflowSimulationMockAttempt `json:"mock_item_attempts,omitempty"`
	MockAttempts     map[string][]StoredWorkflowSimulationMockAttempt            `json:"mock_attempts,omitempty"`
}

func TestInputsFromSimulation(sim SimulateStoredWorkflowRequest) StoredWorkflowTestInputs {
	return StoredWorkflowTestInputs{Input: sim.Input, MockOutputs: sim.MockOutputs, MockItemOutputs: sim.MockItemOutputs,
		MockItemAttempts: sim.MockItemAttempts, MockAttempts: sim.MockAttempts}
}

func (inputs StoredWorkflowTestInputs) Simulation(def WorkflowSpec) SimulateStoredWorkflowRequest {
	return SimulateStoredWorkflowRequest{Definition: def, Input: inputs.Input, MockOutputs: inputs.MockOutputs,
		MockItemOutputs: inputs.MockItemOutputs, MockItemAttempts: inputs.MockItemAttempts, MockAttempts: inputs.MockAttempts}
}

type StoredWorkflowTestCase struct {
	Name         string                           `json:"name"`
	Simulation   StoredWorkflowTestInputs         `json:"simulation"`
	Expectations []StoredWorkflowCheckExpectation `json:"expectations"`
	Live         *StoredWorkflowLiveTest          `json:"live,omitempty"`
}
type StoredWorkflowTestSuite struct {
	Sample StoredWorkflowTestInputs `json:"sample"`
	Cases  []StoredWorkflowTestCase `json:"cases"`
}
type SaveStoredWorkflowTestSuiteRequest struct {
	ExpectedVersion int64                    `json:"expected_version"`
	Suite           *StoredWorkflowTestSuite `json:"suite"` // null clears saved data and results
}
type RunStoredWorkflowTestsRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

// StoredWorkflowTestReport retains safe case/step summaries, without mock payloads,
// outputs, errors or a publication receipt.
type StoredWorkflowTestReport struct {
	CheckedVersion int64                          `json:"checked_version"`
	CheckedAt      time.Time                      `json:"checked_at"`
	InputsHash     string                         `json:"inputs_hash"`
	Passed         bool                           `json:"passed"`
	Cases          []StoredWorkflowTestCaseResult `json:"cases"`
}
type StoredWorkflowTestCaseResult struct {
	Name           string                         `json:"name"`
	Passed         bool                           `json:"passed"`
	Issue          string                         `json:"issue,omitempty"`
	StepsTruncated bool                           `json:"steps_truncated,omitempty"`
	Steps          []StoredWorkflowTestStepResult `json:"steps"`
}
type StoredWorkflowTestStepResult struct {
	Step       string   `json:"step"`
	Loop       string   `json:"loop,omitempty"`
	ItemIndex  *int     `json:"item_index,omitempty"`
	State      string   `json:"state,omitempty"`
	Passed     bool     `json:"passed"`
	Mismatches []string `json:"mismatches,omitempty"`
}

// StoredWorkflowTestInputsHash tolerates JSONB object-key ordering without losing
// numeric precision. It deliberately preserves array order and number literals.
func StoredWorkflowTestInputsHash(def WorkflowSpec, suite StoredWorkflowTestSuite) (string, error) {
	raw, err := json.Marshal(struct {
		Definition WorkflowSpec
		Suite      StoredWorkflowTestSuite
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

func (c *Client) SaveStoredWorkflowTestSuite(ctx context.Context, slug, name string, body SaveStoredWorkflowTestSuiteRequest) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	err := c.do(ctx, "PUT", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/tests", body, &out)
	return out, err
}
func (c *Client) RunStoredWorkflowTests(ctx context.Context, slug, name string, body RunStoredWorkflowTestsRequest) (StoredWorkflowTestReport, error) {
	var out StoredWorkflowTestReport
	err := c.do(ctx, "POST", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/tests/run", body, &out)
	return out, err
}

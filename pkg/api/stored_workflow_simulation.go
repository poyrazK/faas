package api

import (
	"context"
	"encoding/json"
)

// SimulateStoredWorkflowRequest supplies sample data, never execution authority.
type SimulateStoredWorkflowRequest struct {
	Definition       WorkflowSpec                                                `json:"definition"`
	Input            json.RawMessage                                             `json:"input,omitempty"`
	MockOutputs      map[string]json.RawMessage                                  `json:"mock_outputs,omitempty"`
	MockItemOutputs  map[string][]json.RawMessage                                `json:"mock_item_outputs,omitempty"`
	MockItemAttempts map[string]map[string][]StoredWorkflowSimulationMockAttempt `json:"mock_item_attempts,omitempty"`
	MockAttempts     map[string][]StoredWorkflowSimulationMockAttempt            `json:"mock_attempts,omitempty"`
}

// StoredWorkflowSimulationMockAttempt supplies one hypothetical action or wait
// outcome. Exactly one outcome shape is accepted: output, error/http_status,
// or timeout.
// Event and callback waits accept one success with the received payload as output,
// or one timeout when a timeout route is configured. Other waits accept timeout only.
type StoredWorkflowSimulationMockAttempt struct {
	Outcome    string          `json:"outcome"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	HTTPStatus *int            `json:"http_status,omitempty"`
}

// SimulateStoredWorkflowResponse is a bounded hypothetical trace using supplied mocks.
type SimulateStoredWorkflowResponse struct {
	DefinitionValid bool                           `json:"definition_valid"`
	DefinitionHash  string                         `json:"definition_hash"`
	Complete        bool                           `json:"complete"`
	Issues          []string                       `json:"issues"`
	Warnings        []string                       `json:"warnings"`
	StepOrder       []string                       `json:"step_order"`
	Trace           []StoredWorkflowSimulationStep `json:"trace"`
}

// StoredWorkflowSimulationStep describes a control decision or an unexecuted action.
type StoredWorkflowSimulationStep struct {
	StepName      string                            `json:"step_name"`
	Kind          string                            `json:"kind"`
	State         string                            `json:"state"`
	Reason        string                            `json:"reason,omitempty"`
	BlockedBy     []string                          `json:"blocked_by,omitempty"`
	WhenMatched   *bool                             `json:"when_matched,omitempty"`
	Input         json.RawMessage                   `json:"input,omitempty"`
	Output        json.RawMessage                   `json:"output,omitempty"`
	Run           string                            `json:"run,omitempty"`
	Path          string                            `json:"path,omitempty"`
	RawQuery      string                            `json:"raw_query,omitempty"`
	Method        string                            `json:"method,omitempty"`
	IntegrationID string                            `json:"integration_id,omitempty"`
	WaitFor       string                            `json:"wait_for,omitempty"`
	ParentStep    string                            `json:"parent_step,omitempty"`
	ItemIndex     *int                              `json:"item_index,omitempty"`
	ItemCount     *int                              `json:"item_count,omitempty"`
	Attempts      []StoredWorkflowSimulationAttempt `json:"attempts,omitempty"`
}

// StoredWorkflowSimulationAttempt is the safe summary of one supplied outcome.
// Mocked error text is available only through an eligible failure context.
type StoredWorkflowSimulationAttempt struct {
	Attempt    int    `json:"attempt"`
	Outcome    string `json:"outcome"`
	HTTPStatus *int   `json:"http_status,omitempty"`
}

func (c *Client) SimulateStoredWorkflow(ctx context.Context, slug string, body SimulateStoredWorkflowRequest) (SimulateStoredWorkflowResponse, error) {
	var out SimulateStoredWorkflowResponse
	err := c.do(ctx, "POST", storedWorkflowPath(slug)+":simulate", body, &out)
	return out, err
}

package api

import (
	"context"
	"encoding/json"
)

// SimulateAutomationRequest supplies sample data, never execution authority.
type SimulateAutomationRequest struct {
	Definition      WorkflowSpec                 `json:"definition"`
	Input           json.RawMessage              `json:"input,omitempty"`
	MockOutputs     map[string]json.RawMessage   `json:"mock_outputs,omitempty"`
	MockItemOutputs map[string][]json.RawMessage `json:"mock_item_outputs,omitempty"`
}

// SimulateAutomationResponse is a bounded hypothetical trace using supplied mocks.
type SimulateAutomationResponse struct {
	DefinitionValid bool                       `json:"definition_valid"`
	DefinitionHash  string                     `json:"definition_hash"`
	Complete        bool                       `json:"complete"`
	Issues          []string                   `json:"issues"`
	Warnings        []string                   `json:"warnings"`
	StepOrder       []string                   `json:"step_order"`
	Trace           []AutomationSimulationStep `json:"trace"`
}

// AutomationSimulationStep describes a control decision or an unexecuted action.
type AutomationSimulationStep struct {
	StepName      string          `json:"step_name"`
	Kind          string          `json:"kind"`
	State         string          `json:"state"`
	Reason        string          `json:"reason,omitempty"`
	BlockedBy     []string        `json:"blocked_by,omitempty"`
	WhenMatched   *bool           `json:"when_matched,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	Run           string          `json:"run,omitempty"`
	Path          string          `json:"path,omitempty"`
	Method        string          `json:"method,omitempty"`
	IntegrationID string          `json:"integration_id,omitempty"`
	WaitFor       string          `json:"wait_for,omitempty"`
	ParentStep    string          `json:"parent_step,omitempty"`
	ItemIndex     *int            `json:"item_index,omitempty"`
	ItemCount     *int            `json:"item_count,omitempty"`
}

func (c *Client) SimulateAutomation(ctx context.Context, slug string, body SimulateAutomationRequest) (SimulateAutomationResponse, error) {
	var out SimulateAutomationResponse
	err := c.do(ctx, "POST", automationPath(slug)+":simulate", body, &out)
	return out, err
}

// adr: 20261019001500
package api

import (
	"encoding/json"
	"time"
)

// An approval form names the structured input an approver submits with a
// decision. These types extend the public approval types without changing
// them while the feature is gated.
const OperationApprovalInputMaxBytes = 16 << 10

// OperationApprovalFormRequest sets the form for one approval step of an
// Operation name. Scope defaults to the default environment.
type OperationApprovalFormRequest struct {
	Scope       string          `json:"scope,omitempty"`
	Operation   string          `json:"operation"`
	Step        string          `json:"step"`
	InputSchema json.RawMessage `json:"input_schema"`
	Required    bool            `json:"required,omitempty"`
}

type OperationApprovalForm struct {
	ID          string          `json:"id"`
	AppID       string          `json:"app_id"`
	Scope       string          `json:"scope"`
	Operation   string          `json:"operation"`
	Step        string          `json:"step"`
	InputSchema json.RawMessage `json:"input_schema"`
	Required    bool            `json:"required"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type OperationApprovalFormList struct {
	Forms []OperationApprovalForm `json:"forms"`
}

// OperationApprovalInputRequest is OperationApprovalRequest with input.
type OperationApprovalInputRequest struct {
	ExpectedGeneration int             `json:"expected_generation"`
	Decision           string          `json:"decision"`
	Comment            string          `json:"comment,omitempty"`
	Input              json.RawMessage `json:"input,omitempty"`
}

// OperationApprovalInputDecision is OperationApprovalDecision with input.
type OperationApprovalInputDecision struct {
	Step     string                 `json:"step"`
	Decision string                 `json:"decision"`
	Actor    OperationApprovalActor `json:"actor"`
	Comment  string                 `json:"comment,omitempty"`
	Input    json.RawMessage        `json:"input,omitempty"`
}

// OperationApprovalInputPayload is the callback payload of a decision with
// input; without input it encodes exactly as OperationApprovalPayload.
type OperationApprovalInputPayload struct {
	Approval OperationApprovalInputDecision `json:"approval"`
}

// OperationApprovalFormView is one Operation's approval step with its form
// and, once decided, the recorded decision.
type OperationApprovalFormView struct {
	Step        string                          `json:"step"`
	Approver    string                          `json:"approver"`
	InputSchema json.RawMessage                 `json:"input_schema"`
	Required    bool                            `json:"required"`
	Decision    *OperationApprovalInputDecision `json:"decision,omitempty"`
}

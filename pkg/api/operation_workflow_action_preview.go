package api

import "time"

type OperationWorkflowActionPreviewRequest struct {
	AppID           string           `json:"app_id,omitempty"`
	TenantID        string           `json:"tenant_id,omitempty"`
	Scope           string           `json:"scope"`
	Subject         OperationSubject `json:"subject"`
	Workflow        string           `json:"workflow"`
	InstanceID      string           `json:"instance_id"`
	Operation       string           `json:"operation,omitempty"`
	StateRevision   int64            `json:"state_revision,omitempty"`
	ContractVersion int              `json:"contract_version,omitempty"`
}

// Actions are candidates evaluated with an empty evidence plan, not executable commands.
type OperationWorkflowActionPreviewResponse struct {
	Subject         OperationSubject                       `json:"subject"`
	Workflow        string                                 `json:"workflow"`
	InstanceID      string                                 `json:"instance_id"`
	EvaluatedAt     time.Time                              `json:"evaluated_at"`
	State           *OperationWorkflowState                `json:"state,omitempty"`
	StateRevision   int64                                  `json:"state_revision,omitempty"`
	ContractVersion int                                    `json:"contract_version"`
	Reason          string                                 `json:"reason"`
	Actions         []OperationWorkflowTransitionReadiness `json:"actions"`
	ActionCount     int                                    `json:"action_count"`
	HasMore         bool                                   `json:"has_more"`
}

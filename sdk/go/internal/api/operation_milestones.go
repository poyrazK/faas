// ADR-715: durable business facts are independent of execution attempts.
package api

import (
	"encoding/json"
	"time"
)

type OperationMilestoneRequest struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type OperationMilestoneValidationRequest struct {
	Milestones []OperationMilestoneRequest `json:"milestones"`
}

type OperationMilestoneValidationResponse struct {
	Valid bool `json:"valid"`
}

type OperationWorkflowStateReport struct {
	ID                 string                               `json:"id"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	FromState          string                               `json:"from_state,omitempty"`
	State              string                               `json:"state"`
	Revision           int64                                `json:"revision"`
	OccurredAt         time.Time                            `json:"occurred_at"`
	ContractVersion    int                                  `json:"contract_version,omitempty"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
}

type OperationWorkflowEvidenceMilestone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OperationWorkflowStateValidationRequest struct {
	WorkflowStates []OperationWorkflowStateReport `json:"workflow_states"`
	Milestones     []OperationMilestoneRequest    `json:"milestones,omitempty"`
}

type OperationWorkflowStateValidationResponse struct {
	Valid bool `json:"valid"`
}

type OperationWorkflowStateReportResponse struct {
	ID                 string                               `json:"id"`
	OperationID        string                               `json:"operation_id"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	FromState          string                               `json:"from_state,omitempty"`
	State              string                               `json:"state"`
	Revision           int64                                `json:"revision"`
	ContractVersion    int                                  `json:"contract_version"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
}

type OperationWorkflowState struct {
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	State              string                               `json:"state"`
	Terminal           bool                                 `json:"terminal"`
	Stale              bool                                 `json:"stale"`
	OccurredAt         time.Time                            `json:"occurred_at"`
	StaleAfterSeconds  int64                                `json:"stale_after_seconds,omitempty"`
	Revision           int64                                `json:"revision"`
	ContractVersion    int                                  `json:"contract_version"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
	UpdatedAt          time.Time                            `json:"updated_at"`
	PlatformTenantID   string                               `json:"platform_tenant_id,omitempty"`
}

type OperationWorkflowStateHistoryEntry struct {
	ID                 string                               `json:"id"`
	OperationID        string                               `json:"operation_id"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	FromState          string                               `json:"from_state,omitempty"`
	State              string                               `json:"state"`
	Revision           int64                                `json:"revision"`
	ContractVersion    int                                  `json:"contract_version"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
	OccurredAt         time.Time                            `json:"occurred_at"`
	PublishedAt        time.Time                            `json:"published_at"`
	PlatformTenantID   string                               `json:"platform_tenant_id,omitempty"`
}

type OperationMilestone struct {
	WorkflowSteps    []OperationWorkflowSpec `json:"workflow_steps,omitempty"`
	ID               string                  `json:"id"`
	OperationID      string                  `json:"operation_id"`
	PlatformTenantID string                  `json:"platform_tenant_id,omitempty"`
	Subject          *OperationSubject       `json:"subject,omitempty"`
	Name             string                  `json:"name"`
	Payload          json.RawMessage         `json:"payload"`
	OccurredAt       time.Time               `json:"occurred_at"`
	CreatedAt        time.Time               `json:"created_at"`
	Sequence         int64                   `json:"sequence"`
}

type OperationMilestonesResponse struct {
	Milestones              []OperationMilestone                 `json:"milestones"`
	WorkflowStates          []OperationWorkflowState             `json:"workflow_states,omitempty"`
	WorkflowStateHistory    []OperationWorkflowStateHistoryEntry `json:"workflow_state_history,omitempty"`
	NextCursor              string                               `json:"next_cursor,omitempty"`
	NextWorkflowStateCursor string                               `json:"next_workflow_state_cursor,omitempty"`
}

type OperationMilestoneListOptions struct {
	AppID       string
	Scope       string
	OperationID string
	SubjectType string
	SubjectID   string
	// Workflow and WorkflowInstanceID are paired filters for a business-reference feed.
	Workflow            string
	WorkflowInstanceID  string
	WorkflowStateCursor string
	WorkflowStaleOnly   bool
	TenantID            string // Account operator filter; never a tenant-self owner override.
	Limit               int
	Cursor              string
}

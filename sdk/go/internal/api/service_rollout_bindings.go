package api

import "time"

// ServiceRolloutBindingGate is a bounded projection of a routing check. It is
// status and audit evidence, never a reusable authorization for another switch.
type ServiceRolloutBindingGate struct {
	RequestID      string                `json:"request_id"`
	Action         string                `json:"action"`
	DeploymentID   string                `json:"deployment_id"`
	Status         string                `json:"status"`
	Code           string                `json:"code,omitempty"`
	Blockers       []BindingCheckFinding `json:"blockers,omitempty"`
	CheckedAt      *time.Time            `json:"checked_at,omitempty"`
	PolicyRevision int64                 `json:"policy_revision,omitempty"`
	AuditID        string                `json:"audit_id,omitempty"`
}

// ServiceRolloutRecoveryReceipt confirms accepted reverse-handoff intent.
// Routing, acknowledgements and request draining complete asynchronously.
type ServiceRolloutRecoveryReceipt struct {
	DeploymentID            string `json:"deployment_id"`
	PredecessorDeploymentID string `json:"predecessor_deployment_id"`
	RequestID               string `json:"request_id"`
	Status                  string `json:"status"`
}

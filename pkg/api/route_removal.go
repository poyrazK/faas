package api

import "time"

// RouteRemovalPolicy applies to production traffic. Revision zero is the
// implicit report policy; updates require the current revision.
type RouteRemovalPolicy struct {
	AppID                string     `json:"app_id"`
	Mode                 string     `json:"mode"`
	Revision             int64      `json:"revision"`
	GracePeriod          string     `json:"grace_period"`
	MaxApprovalAge       string     `json:"max_approval_age"`
	BaselineDeploymentID string     `json:"baseline_deployment_id,omitempty"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty"`
}
type SetRouteRemovalPolicyRequest struct {
	ExpectedRevision *int64 `json:"expected_revision"`
	Mode             string `json:"mode"`
	GracePeriod      string `json:"grace_period"`
	MaxApprovalAge   string `json:"max_approval_age"`
}
type RouteRemovalMapping struct {
	Method          string `json:"method"`
	Path            string `json:"path"`
	SuccessorMethod string `json:"successor_method,omitempty"`
	SuccessorPath   string `json:"successor_path,omitempty"`
}

// Approvals use server-owned captures and telemetry. No caller-provided owner,
// timestamp, compatibility status or traffic count is accepted.
type ApproveRouteRemovalRequest struct {
	ExpectedPolicyRevision  *int64                `json:"expected_policy_revision"`
	BaselineDeploymentID    string                `json:"baseline_deployment_id"`
	CandidateDeploymentID   string                `json:"candidate_deployment_id"`
	BaselineContractSHA256  string                `json:"baseline_contract_sha256"`
	CandidateContractSHA256 string                `json:"candidate_contract_sha256"`
	Mappings                []RouteRemovalMapping `json:"mappings"`
	AcknowledgeObservedOnly bool                  `json:"acknowledge_observed_only"`
}
type RouteRemovalApproval struct {
	ID                      string                `json:"id"`
	AppID                   string                `json:"app_id"`
	PolicyRevision          int64                 `json:"policy_revision"`
	BaselineDeploymentID    string                `json:"baseline_deployment_id"`
	CandidateDeploymentID   string                `json:"candidate_deployment_id"`
	BaselineContractSHA256  string                `json:"baseline_contract_sha256"`
	CandidateContractSHA256 string                `json:"candidate_contract_sha256"`
	MappingSHA256           string                `json:"mapping_sha256"`
	Mappings                []RouteRemovalMapping `json:"mappings"`
	ApprovedBy              string                `json:"approved_by"`
	ApprovedAt              time.Time             `json:"approved_at"`
	ValidUntil              time.Time             `json:"valid_until"`
	ObservationFrom         time.Time             `json:"observation_from"`
	ObservationUntil        time.Time             `json:"observation_until"`
	Coverage                string                `json:"coverage"`
}
type RouteRemovalCheck struct {
	EarliestApprovalAt      *time.Time            `json:"earliest_approval_at,omitempty"`
	ApprovalValidUntil      *time.Time            `json:"approval_valid_until,omitempty"`
	NextActions             []string              `json:"next_actions,omitempty"`
	BaselineContractSHA256  string                `json:"baseline_contract_sha256,omitempty"`
	CandidateContractSHA256 string                `json:"candidate_contract_sha256,omitempty"`
	Policy                  RouteRemovalPolicy    `json:"policy"`
	CandidateDeploymentID   string                `json:"candidate_deployment_id"`
	Status                  string                `json:"status"`
	Removed                 []RouteRemovalMapping `json:"removed"`
	Blockers                []string              `json:"blockers"`
	ApprovalID              string                `json:"approval_id,omitempty"`
}

const CodeRouteRemovalRequired = "route_removal_required"
const CodeRouteRemovalPolicyChanged = "route_removal_policy_changed"

package api

import "time"

// Successor mappings are explicit; a URL alone cannot identify a compatible operation.
type RouteLifecycleMapping struct {
	Method                  string `json:"method"`
	Path                    string `json:"path"`
	SuccessorURL            string `json:"successor_url"`
	SuccessorMethod         string `json:"successor_method"`
	SuccessorPath           string `json:"successor_path"`
	SuccessorAppID          string `json:"successor_app_id,omitempty"`
	SuccessorDeploymentID   string `json:"successor_deployment_id,omitempty"`
	SuccessorContractSHA256 string `json:"successor_contract_sha256,omitempty"`
}

type ApproveRouteLifecycleRequest struct {
	ExpectedGateRevision          *int64                  `json:"expected_gate_revision"`
	ExpectedRequirementsRevision  *int64                  `json:"expected_requirements_revision"`
	ExpectedRemovalPolicyRevision *int64                  `json:"expected_removal_policy_revision"`
	ConfigurationSHA256           string                  `json:"configuration_sha256"`
	BaselineDeploymentID          string                  `json:"baseline_deployment_id"`
	CandidateDeploymentID         string                  `json:"candidate_deployment_id"`
	BaselineContractSHA256        string                  `json:"baseline_contract_sha256"`
	CandidateContractSHA256       string                  `json:"candidate_contract_sha256"`
	Mappings                      []RouteLifecycleMapping `json:"mappings"`
}

type RouteLifecycleApproval struct {
	ID                      string                  `json:"id"`
	AppID                   string                  `json:"app_id"`
	GateRevision            int64                   `json:"gate_revision"`
	RequirementsRevision    int64                   `json:"requirements_revision"`
	RemovalPolicyRevision   int64                   `json:"removal_policy_revision"`
	ConfigurationSHA256     string                  `json:"configuration_sha256"`
	BaselineDeploymentID    string                  `json:"baseline_deployment_id"`
	CandidateDeploymentID   string                  `json:"candidate_deployment_id"`
	BaselineContractSHA256  string                  `json:"baseline_contract_sha256"`
	CandidateContractSHA256 string                  `json:"candidate_contract_sha256"`
	MappingSHA256           string                  `json:"mapping_sha256"`
	Mappings                []RouteLifecycleMapping `json:"mappings"`
	Compatibility           string                  `json:"compatibility"`
	CheckerVersion          int                     `json:"checker_version"`
	ApprovedBy              string                  `json:"approved_by"`
	ApprovedAt              time.Time               `json:"approved_at"`
	ValidUntil              time.Time               `json:"valid_until"`
	InvalidatedAt           *time.Time              `json:"invalidated_at,omitempty"`
}

const CodeRouteLifecycleReviewRequired = "route_lifecycle_review_required"
const CodeRouteLifecycleReviewChanged = "route_lifecycle_review_changed"

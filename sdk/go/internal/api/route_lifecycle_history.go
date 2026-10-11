package api

import "time"

type RouteLifecycleHistoryCapture struct {
	DeploymentID string `json:"deployment_id"`
	SHA256       string `json:"sha256"`
}
type RouteLifecycleHistoryApproval struct {
	ID                      string     `json:"id"`
	Used                    bool       `json:"used"`
	Status                  string     `json:"status"`
	StatusReason            string     `json:"status_reason"`
	BaselineDeploymentID    string     `json:"baseline_deployment_id"`
	CandidateDeploymentID   string     `json:"candidate_deployment_id"`
	BaselineContractSHA256  string     `json:"baseline_contract_sha256"`
	CandidateContractSHA256 string     `json:"candidate_contract_sha256"`
	ConfigurationSHA256     string     `json:"configuration_sha256"`
	ValidUntil              time.Time  `json:"valid_until"`
	InvalidatedAt           *time.Time `json:"invalidated_at,omitempty"`
	GraphIDs                []string   `json:"graph_ids"`
}
type RouteLifecycleHistoryEntry struct {
	ID                string                          `json:"id"`
	AppID             string                          `json:"app_id"`
	DeploymentID      string                          `json:"deployment_id"`
	ReviewedAt        time.Time                       `json:"reviewed_at"`
	Scope             string                          `json:"scope"`
	Outcome           string                          `json:"outcome"`
	Recovery          bool                            `json:"recovery"`
	Decision          RouteGateDecision               `json:"decision"`
	EvidenceAvailable bool                            `json:"evidence_available"`
	Truncated         bool                            `json:"truncated"`
	Captures          []RouteLifecycleHistoryCapture  `json:"captures"`
	GraphIDs          []string                        `json:"graph_ids"`
	Approvals         []RouteLifecycleHistoryApproval `json:"approvals"`
}
type RouteLifecycleHistoryPage struct {
	AppID      string                       `json:"app_id"`
	Entries    []RouteLifecycleHistoryEntry `json:"entries"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

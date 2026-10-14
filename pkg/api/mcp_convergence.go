package api

import "time"

const (
	MCPPolicyGatewayFreshness       = 10 * time.Second
	MCPPolicyConvergenceReadTimeout = 5 * time.Second
)

func IsMCPPolicyConvergenceMetric(metric string) bool {
	return metric == "mcp_policy_unconverged_gateways" || metric == "mcp_policy_unverified"
}

type MCPPolicyConvergenceSnapshot struct {
	State                string    `json:"state"`
	CapturedAt           time.Time `json:"captured_at"`
	DesiredRevision      int64     `json:"desired_revision"`
	ServingGateways      int64     `json:"serving_gateways"`
	PendingGateways      int64     `json:"pending_gateways"`
	StaleGateways        int64     `json:"stale_gateways"`
	UnconvergedGateways  int64     `json:"unconverged_gateways"`
	DeploymentID         string    `json:"deployment_id,omitempty"`
	AdmissionID          string    `json:"admission_id,omitempty"`
	PolicyStatusPath     string    `json:"policy_status_path,omitempty"`
	AdmissionHistoryPath string    `json:"admission_history_path,omitempty"`
}

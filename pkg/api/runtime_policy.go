package api

// RuntimePolicyStatusResponse reports whether the registered serving gateway
// fleet has applied app-cache and deployment traffic changes. Edge-rule
// convergence is reported separately because it has its own revision
// sequence. It does not attest scheduler, VM, or guest-side policy
// convergence.
type RuntimePolicyStatusResponse struct {
	AppID           string                       `json:"app_id"`
	DesiredRevision int64                        `json:"desired_revision"`
	State           string                       `json:"state"` // active, pending, or unverified
	Coverage        []string                     `json:"coverage"`
	ServingGateways int                          `json:"serving_gateways"`
	AppliedGateways int                          `json:"applied_gateways"`
	PendingGateways int                          `json:"pending_gateways"`
	StaleGateways   int                          `json:"stale_gateways"`
	EdgeRules       RuntimePolicyComponentStatus `json:"edge_rules"`
}

// RuntimePolicyComponentStatus reports convergence for a policy ledger with
// its own revision sequence.
type RuntimePolicyComponentStatus struct {
	DesiredRevision int64  `json:"desired_revision"`
	State           string `json:"state"` // active, pending, or unverified
	ServingGateways int    `json:"serving_gateways"`
	AppliedGateways int    `json:"applied_gateways"`
	PendingGateways int    `json:"pending_gateways"`
	StaleGateways   int    `json:"stale_gateways"`
}

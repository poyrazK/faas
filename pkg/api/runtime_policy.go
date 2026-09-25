package api

// RuntimePolicyStatusResponse reports whether the registered serving gateway
// fleet has invalidated app caches and applied deployment traffic weights
// through the desired control-plane revision. It does not attest scheduler,
// VM, or guest-side policy convergence.
type RuntimePolicyStatusResponse struct {
	AppID           string   `json:"app_id"`
	DesiredRevision int64    `json:"desired_revision"`
	State           string   `json:"state"` // active, pending, or unverified
	Coverage        []string `json:"coverage"`
	ServingGateways int      `json:"serving_gateways"`
	AppliedGateways int      `json:"applied_gateways"`
	PendingGateways int      `json:"pending_gateways"`
	StaleGateways   int      `json:"stale_gateways"`
}

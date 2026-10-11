package api

import "time"

// CanaryRouteGate controls traffic advances of an existing canary, not its initial activation.
type CanaryRouteGate struct {
	AppID     string     `json:"app_id"`
	Mode      string     `json:"mode"`
	Revision  int64      `json:"revision"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type SetCanaryRouteGateRequest struct {
	Mode             string `json:"mode"`
	ExpectedRevision *int64 `json:"expected_revision"`
}

// RouteGateDecision contains metadata only; route findings remain in the check API.
type RouteGateDecision struct {
	Mode                 string   `json:"mode"`
	Revision             int64    `json:"revision"`
	DeploymentID         string   `json:"deployment_id"`
	Status               string   `json:"status"`
	Reasons              []string `json:"reasons"`
	LifecycleApprovalIDs []string `json:"lifecycle_approval_ids,omitempty"`
	CheckQueued          bool     `json:"check_queued"`
}

package api

// RouteAdvicePolicyContext records a bounded desired-policy observation.
// It does not establish gateway convergence or authorize a write.
type RouteAdvicePolicyContext struct {
	Status     string                  `json:"status"`
	Revision   string                  `json:"revision,omitempty"`
	ObservedAt string                  `json:"observed_at"`
	Host       string                  `json:"host,omitempty"`
	Rules      []RouteAdvicePolicyRule `json:"rules"`
	Gaps       []string                `json:"gaps"`
}

// RouteAdvicePolicyRule excludes actions, condition values and descriptions.
type RouteAdvicePolicyRule struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Source      string   `json:"source"`
	Path        string   `json:"path"`
	Methods     []string `json:"methods"`
	Priority    int      `json:"priority"`
	Conditional bool     `json:"conditional"`
	Mode        string   `json:"mode"`
	UpdatedAt   string   `json:"updated_at"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
}

// RouteAdvicePolicyAssessment compares a suggestion with selected-app rules.
// No result proves a mutation is safe or substitutes for a guarded write plan.
type RouteAdvicePolicyAssessment struct {
	Status    string   `json:"status"`
	Reason    string   `json:"reason"`
	RuleIDs   []string `json:"rule_ids"`
	Truncated bool     `json:"truncated,omitempty"`
}

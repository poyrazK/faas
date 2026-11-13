package api

import "time"

// AccountHealth is the customer's own view of what may stop their account
// from working (GET /v1/account/health, /dashboard/account/health;
// ADR-20261112210000). It is derived from the operator diagnosis
// (ObsTenantDiagnosis) with internal detail removed: abuse-hold reasons and
// platform flags never appear here.
type AccountHealth struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Attention is the number of restrictions, failed deployments and
	// domain issues; zero means nothing needs action.
	Attention         int                        `json:"attention"`
	Restrictions      []AccountHealthRestriction `json:"restrictions"`
	FailedDeployments []AccountHealthDeployment  `json:"failed_deployments"`
	DomainIssues      []AccountHealthDomain      `json:"domain_issues"`
	RefusedRequests   AccountHealthRequests      `json:"refused_requests"`
	// Unavailable names sections that could not be loaded; their lists are
	// then empty and mean "unknown", not "none".
	Unavailable []string `json:"unavailable,omitempty"`
}

// AccountHealthRestriction is one restriction with customer wording and the
// next step. Kind uses the Restriction* constants.
type AccountHealthRestriction struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
	// ActionURL is a dashboard path for the next step, when there is one.
	ActionURL string     `json:"action_url,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// AccountHealthDeployment is a failed deployment from the last
// ObsDiagnosisDeploymentDays days.
type AccountHealthDeployment struct {
	DeploymentID string    `json:"deployment_id"`
	AppID        string    `json:"app_id"`
	AppSlug      string    `json:"app_slug,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	Hint         string    `json:"hint,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// AccountHealthDomain is a custom domain that is not serving correctly.
type AccountHealthDomain struct {
	Domain  string `json:"domain"`
	AppSlug string `json:"app_slug,omitempty"`
	Problem string `json:"problem"`
	// CertError is the certificate authority's last error, when issuance
	// failed.
	CertError string `json:"cert_error,omitempty"`
}

// AccountHealthRequests summarises the API requests Gregale refused for the
// account in the last WindowHours.
type AccountHealthRequests struct {
	WindowHours int                    `json:"window_hours"`
	Total       int64                  `json:"total"`
	ByCode      []AccountHealthCode    `json:"by_code"`
	Recent      []AccountHealthRequest `json:"recent"`
}

type AccountHealthCode struct {
	Code   string    `json:"code"`
	Count  int64     `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// AccountHealthRequest is one refused request: the matched route pattern,
// the problem code and the correlation ids to quote to support.
type AccountHealthRequest struct {
	OccurredAt time.Time `json:"occurred_at"`
	Route      string    `json:"route"`
	Code       string    `json:"code"`
	AppSlug    string    `json:"app_slug,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	TraceID    string    `json:"trace_id,omitempty"`
}

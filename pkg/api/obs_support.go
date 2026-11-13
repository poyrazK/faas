package api

import "time"

// Operator support visibility DTOs (ADR-20261112170000). They extend the
// /v1/admin/obs/* surface with what a support engineer needs first: which
// account a customer's report is about, and why that account is not working.
// The sensitive-field policy at the top of obs.go applies unchanged: no
// email, secret, response body or request payload appears here.

// Customer error and diagnosis bounds. The recorder keeps rows for
// CustomerErrorRetention; the tenant 360 summarises the last
// CustomerErrorWindow.
const (
	CustomerErrorRetention      = 14 * 24 * time.Hour
	CustomerErrorWindow         = 24 * time.Hour
	CustomerErrorRecentLimit    = 20
	CustomerErrorCodeLimit      = 10
	ObsDiagnosisDeploymentLimit = 10
	ObsDiagnosisDeploymentDays  = 7
	ObsLookupMaxMatches         = 20
	ObsLookupQueryMaxBytes      = 256
)

// ObsLookupResponse answers GET /v1/admin/obs/lookup?q=. Each match names
// the account (and app, when the identifier belongs to one) so the operator
// can open the tenant 360. An empty Matches list means nothing matched.
type ObsLookupResponse struct {
	Query   string           `json:"query"`
	Matches []ObsLookupMatch `json:"matches"`
}

// ObsLookupMatch kinds.
const (
	ObsLookupAccount    = "account"
	ObsLookupApp        = "app"
	ObsLookupDeployment = "deployment"
	ObsLookupDomain     = "domain"
	ObsLookupRequest    = "request"
	ObsLookupTrace      = "trace"
)

type ObsLookupMatch struct {
	// Kind says which identifier matched: account, app, deployment,
	// domain, request or trace.
	Kind      string `json:"kind"`
	AccountID string `json:"account_id"`
	AppID     string `json:"app_id,omitempty"`
	AppSlug   string `json:"app_slug,omitempty"`
	// Detail is a short operator hint, such as "email", "app hostname",
	// "custom domain" or "API error validation_failed".
	Detail string `json:"detail,omitempty"`
	// At is when the matched event happened, for request and trace matches.
	At *time.Time `json:"at,omitempty"`
}

// ObsTenantDiagnosis is the "why is it broken" part of the tenant 360.
// Each list is bounded and ordered newest first. Unavailable names the
// sections that could not be read, so an empty list never hides a failure.
type ObsTenantDiagnosis struct {
	GeneratedAt       time.Time               `json:"generated_at"`
	Restrictions      []ObsTenantRestriction  `json:"restrictions"`
	FailedDeployments []ObsTenantFailedDeploy `json:"failed_deployments"`
	DomainIssues      []ObsTenantDomainIssue  `json:"domain_issues"`
	HealthyDomains    int                     `json:"healthy_domains"`
	EnabledFlags      []string                `json:"enabled_flags"`
	CustomerErrors    ObsCustomerErrorSummary `json:"customer_errors"`
	// AttentionSignals are the account's open or dismissed attention
	// signals (ADR-20261113123000).
	AttentionSignals []ObsAttentionSignal `json:"attention_signals"`
	Unavailable      []string             `json:"unavailable,omitempty"`
}

// ObsTenantRestriction kinds.
const (
	RestrictionAccountSuspended     = "account_suspended"
	RestrictionAccountPastDue       = "account_past_due"
	RestrictionDeletionPending      = "deletion_pending"
	RestrictionAbuseHold            = "abuse_hold"
	RestrictionEmailUnverified      = "email_unverified"
	RestrictionOverageCapReached    = "overage_cap_reached"
	RestrictionPlatformReleasePause = "platform_release_pause"
)

// ObsTenantRestriction is one thing currently stopping the account from
// working normally, such as a suspension, an abuse hold or a reached
// overage cap.
type ObsTenantRestriction struct {
	Kind   string     `json:"kind"`
	Reason string     `json:"reason,omitempty"`
	Since  *time.Time `json:"since,omitempty"`
}

// ObsTenantFailedDeploy is a failed, parked or aborted deployment from the
// last ObsDiagnosisDeploymentDays days, with the error the customer saw.
type ObsTenantFailedDeploy struct {
	DeploymentID string    `json:"deployment_id"`
	AppID        string    `json:"app_id"`
	AppSlug      string    `json:"app_slug,omitempty"`
	Status       string    `json:"status"`
	ErrorCode    string    `json:"error_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	Hint         string    `json:"hint,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ObsTenantDomainIssue is a custom domain that is unverified or whose
// certificate is not serving.
type ObsTenantDomainIssue struct {
	Domain        string     `json:"domain"`
	AppSlug       string     `json:"app_slug,omitempty"`
	Problem       string     `json:"problem"`
	CertStatus    string     `json:"cert_status,omitempty"`
	CertLastError string     `json:"cert_last_error,omitempty"`
	CertExpiresAt *time.Time `json:"cert_expires_at,omitempty"`
}

// ObsCustomerErrorSummary is what the account's API callers were refused
// in the last WindowHours: counts by problem code and the latest rows.
type ObsCustomerErrorSummary struct {
	WindowHours int                     `json:"window_hours"`
	Total       int64                   `json:"total"`
	ByCode      []ObsCustomerErrorCode  `json:"by_code"`
	Recent      []ObsCustomerErrorEvent `json:"recent"`
	// Dropped counts errors this apid replica did not record because the
	// account or the replica exceeded its recording budget since start.
	Dropped int64 `json:"dropped,omitempty"`
}

type ObsCustomerErrorCode struct {
	Code   string    `json:"code"`
	Count  int64     `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// ObsCustomerErrorEvent is one refused control-plane API request. Route is
// the matched route pattern (for example "POST /v1/apps/{slug}/deployments"),
// never the raw URL, so no query string or customer value is stored.
type ObsCustomerErrorEvent struct {
	OccurredAt time.Time `json:"occurred_at"`
	Route      string    `json:"route"`
	Code       string    `json:"code"`
	AppSlug    string    `json:"app_slug,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	TraceID    string    `json:"trace_id,omitempty"`
}

// Attention signal kinds and states (ADR-20261113123000).
const (
	AttentionKindDeployFailing = "deploy_failing"
	AttentionKindRefusedBurst  = "refused_burst"
	AttentionKindDomainStuck   = "domain_stuck"

	AttentionStateOpen      = "open"
	AttentionStateDismissed = "dismissed"
	AttentionStateResolved  = "resolved"
)

// ObsAttentionSignal is one customer who is probably stuck: what kind of
// trouble, on which subject (app slug, problem code or hostname), since
// when, and whether support dismissed it.
type ObsAttentionSignal struct {
	ID        int64  `json:"id"`
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"`
	Subject   string `json:"subject"`
	Detail    string `json:"detail,omitempty"`
	State     string `json:"state"`
	// Observed is the count behind the signal at its last evaluation:
	// failed deployments, refusals or 1 for a domain.
	Observed      int64      `json:"observed"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	DismissedAt   *time.Time `json:"dismissed_at,omitempty"`
	DismissedBy   string     `json:"dismissed_by,omitempty"`
	DismissReason string     `json:"dismiss_reason,omitempty"`
}

// ObsAttentionListResponse answers GET /v1/admin/obs/attention.
type ObsAttentionListResponse struct {
	Items []ObsAttentionSignal `json:"items"`
	Limit int                  `json:"limit"`
}

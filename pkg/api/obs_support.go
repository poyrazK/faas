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
	// ADR-20261115175928 kinds.
	AttentionKindAppOOM         = "app_oom"
	AttentionKindBootFailing    = "boot_failing"
	AttentionKindJobsFailing    = "jobs_failing"
	AttentionKindPaymentFailing = "payment_failing"
	AttentionKindLimitPressure  = "limit_pressure"
	AttentionKindCronsFailing   = "crons_failing"

	AttentionStateOpen      = "open"
	AttentionStateDismissed = "dismissed"
	AttentionStateResolved  = "resolved"

	// Outcomes record what support did about a signal
	// (ADR-20261115175928). Contacting sets contacted; dismissing names one
	// of the next four; a signal that clears without one is resolved_on_own.
	AttentionOutcomeContacted      = "contacted"
	AttentionOutcomePlatformIssue  = "platform_issue"
	AttentionOutcomeAlreadyInTouch = "already_in_touch"
	AttentionOutcomeNotNeeded      = "not_needed"
	AttentionOutcomeFalsePositive  = "false_positive"
	AttentionOutcomeResolvedOnOwn  = "resolved_on_own"
)

// AttentionKinds lists every signal kind in display order.
func AttentionKinds() []string {
	return []string{AttentionKindDeployFailing, AttentionKindAppOOM, AttentionKindBootFailing, AttentionKindJobsFailing, AttentionKindCronsFailing,
		AttentionKindDomainStuck, AttentionKindRefusedBurst, AttentionKindLimitPressure, AttentionKindPaymentFailing}
}

// AttentionDismissOutcomes are the outcomes staff pick when dismissing.
func AttentionDismissOutcomes() []string {
	return []string{AttentionOutcomePlatformIssue, AttentionOutcomeAlreadyInTouch, AttentionOutcomeNotNeeded, AttentionOutcomeFalsePositive}
}

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
	// failed deployments, refusals, OOM kills, failed boots, failed runs,
	// days with limit refusals, failed payments, or 1 for a domain.
	Observed      int64      `json:"observed"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	DismissedAt   *time.Time `json:"dismissed_at,omitempty"`
	DismissedBy   string     `json:"dismissed_by,omitempty"`
	DismissReason string     `json:"dismiss_reason,omitempty"`
	// Outcome is what support did (contacted, platform_issue, ...);
	// TicketID is the ticket support opened when it contacted the customer.
	Outcome  string `json:"outcome,omitempty"`
	TicketID string `json:"ticket_id,omitempty"`
}

// ObsAttentionListResponse answers GET /v1/admin/obs/attention.
type ObsAttentionListResponse struct {
	Items []ObsAttentionSignal `json:"items"`
	Limit int                  `json:"limit"`
}

// Operator console signal actions (ADR-20261115175927): the JSON twins of
// the staff page at /dashboard/admin/support/attention.

// ObsAttentionDismissRequest is the body of
// POST /v1/admin/obs/attention/{id}/dismiss. Outcome is one of
// AttentionDismissOutcomes; Note is optional, at most
// AttentionDismissReasonMax bytes.
type ObsAttentionDismissRequest struct {
	Outcome string `json:"outcome"`
	Note    string `json:"note,omitempty"`
}

// ObsAttentionContactRequest is the body of
// POST /v1/admin/obs/attention/{id}/contact. The Idempotency-Key header is
// also the support ticket's idempotency key, so a retry replays the ticket.
type ObsAttentionContactRequest struct {
	Subject  string `json:"subject"`
	Category string `json:"category"`
	Body     string `json:"body"`
}

// ObsAttentionActionResponse answers a dismiss or contact with the signal
// as it now stands and, after a contact, the ticket that was opened.
type ObsAttentionActionResponse struct {
	Signal   ObsAttentionSignal `json:"signal"`
	TicketID string             `json:"ticket_id,omitempty"`
}

// ObsAttentionContactDraft answers
// GET /v1/admin/obs/attention/{id}/contact-draft: the prefilled message for
// an open signal, worded with only what the customer can see in their own
// dashboard.
type ObsAttentionContactDraft struct {
	Subject    string   `json:"subject"`
	Category   string   `json:"category"`
	Body       string   `json:"body"`
	Categories []string `json:"categories"`
	SubjectMax int      `json:"subject_max"`
	BodyMax    int      `json:"body_max"`
}

// ObsAttentionKindSummary is how one signal kind fared: how many opened,
// what support did and how long it took to act. FalsePositivePercent is -1
// when nobody acted on any; MedianActSeconds is absent then too.
type ObsAttentionKindSummary struct {
	Kind           string `json:"kind"`
	Total          int64  `json:"total"`
	Open           int64  `json:"open"`
	Contacted      int64  `json:"contacted"`
	FalsePositive  int64  `json:"false_positive"`
	OtherDismissed int64  `json:"other_dismissed"`
	ResolvedOnOwn  int64  `json:"resolved_on_own"`
	// NoOutcome counts closed signals with no recorded outcome (closed
	// before outcomes existed); the counts above plus this one sum to Total.
	NoOutcome            int64    `json:"no_outcome,omitempty"`
	FalsePositivePercent int64    `json:"false_positive_percent"`
	MedianActSeconds     *float64 `json:"median_act_seconds,omitempty"`
}

// ObsAttentionSummaryResponse answers GET /v1/admin/obs/attention/summary:
// signals first seen in the last WindowDays, one row per kind that had any.
type ObsAttentionSummaryResponse struct {
	WindowDays int                       `json:"window_days"`
	Items      []ObsAttentionKindSummary `json:"items"`
}

// ObsSupportTicketSnapshot is the customer's own account-health view saved
// when they opened a ticket (ADR-20261112210000), with the refused request
// matching the request id they attached.
type ObsSupportTicketSnapshot struct {
	CapturedAt time.Time             `json:"captured_at"`
	Plan       string                `json:"plan"`
	Status     string                `json:"status"`
	Health     AccountHealth         `json:"health"`
	Request    *AccountHealthRequest `json:"request,omitempty"`
	RequestID  string                `json:"request_id,omitempty"`
}

// ObsSupportTicketCustomer answers GET /v1/admin/support/tickets/{ticket}/customer:
// the account behind a ticket, its live diagnosis, the snapshot taken when
// the ticket was opened, and the restriction kinds lifted or added since
// (ADR-20261115175927). Unavailable means the account could not be loaded;
// only the ticket id is then meaningful.
type ObsSupportTicketCustomer struct {
	TicketID    string                    `json:"ticket_id"`
	Unavailable bool                      `json:"unavailable,omitempty"`
	AccountID   string                    `json:"account_id,omitempty"`
	OrgSlug     string                    `json:"org_slug,omitempty"`
	Plan        string                    `json:"plan,omitempty"`
	Status      string                    `json:"status,omitempty"`
	Apps        int                       `json:"apps"`
	Diagnosis   *ObsTenantDiagnosis       `json:"diagnosis,omitempty"`
	Snapshot    *ObsSupportTicketSnapshot `json:"snapshot,omitempty"`
	Lifted      []string                  `json:"lifted,omitempty"`
	Added       []string                  `json:"added,omitempty"`
}

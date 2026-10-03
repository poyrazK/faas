package api

import "time"

// RouteMonitorRoute selects an exact gateway-normalized route and absolute budgets.
// A nil error budget disables that signal; zero permits no sustained 5xx errors.
type RouteMonitorRoute struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	Max5xxRateBPS *int64 `json:"max_5xx_rate_bps,omitempty"`
	MaxP95MS      int64  `json:"max_p95_ms,omitempty"`
}
type RouteMonitorConfig struct {
	CustomerGroupBy string              `json:"customer_group_by,omitempty"`
	AppID           string              `json:"app_id"`
	Enabled         bool                `json:"enabled"`
	Revision        int64               `json:"revision"`
	Routes          []RouteMonitorRoute `json:"routes"`
	UpdatedAt       *time.Time          `json:"updated_at,omitempty"`
}
type SetRouteMonitorRequest struct {
	CustomerGroupBy  string              `json:"customer_group_by,omitempty"`
	Enabled          bool                `json:"enabled"`
	ExpectedRevision *int64              `json:"expected_revision"`
	Routes           []RouteMonitorRoute `json:"routes"`
}
type RouteMonitorWindow struct {
	Start         time.Time         `json:"start"`
	End           time.Time         `json:"end"`
	Observed      RouteHealthCounts `json:"observed"`
	ErrorStatus   string            `json:"error_status"`
	ErrorReason   string            `json:"error_reason"`
	LatencyStatus string            `json:"latency_status"`
	LatencyReason string            `json:"latency_reason"`
}
type RouteMonitorFinding struct {
	Route         RouteMonitorRoute    `json:"route"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason"`
	ErrorStatus   string               `json:"error_status"`
	LatencyStatus string               `json:"latency_status"`
	Windows       []RouteMonitorWindow `json:"windows"`
}

// Monitoring describes stored observations, not a complete capture or an SLO.
type RouteMonitorReport struct {
	CustomerGroupBy        string                      `json:"customer_group_by,omitempty"`
	Customers              *RouteMonitorCustomerReport `json:"customers,omitempty"`
	Version                int                         `json:"version"`
	AppID                  string                      `json:"app_id"`
	Enabled                bool                        `json:"enabled"`
	Revision               int64                       `json:"revision"`
	DeploymentID           string                      `json:"deployment_id,omitempty"`
	CommitSHA              string                      `json:"commit_sha,omitempty"`
	CheckedAt              time.Time                   `json:"checked_at"`
	ObservationAnchor      *time.Time                  `json:"observation_anchor,omitempty"`
	Coverage               string                      `json:"coverage"`
	Status                 string                      `json:"status"`
	Reason                 string                      `json:"reason"`
	MinimumRequests        int64                       `json:"minimum_requests"`
	MinimumLatencyRequests int64                       `json:"minimum_latency_requests"`
	Routes                 []RouteMonitorFinding       `json:"routes"`
}
type RouteMonitorEvidenceWindow struct {
	Start       time.Time                      `json:"start"`
	End         time.Time                      `json:"end"`
	Requests    RouteHealthInvestigationSide   `json:"requests"`
	Diagnostics *RouteHealthLatencyDiagnostics `json:"diagnostics,omitempty"`
}

// Diagnostics reuse the debugger's candidate side for the monitored deployment;
// the stable side is empty and there is no comparative or causal claim.
type RouteMonitorEvidence struct {
	CustomerGroupBy string                       `json:"customer_group_by,omitempty"`
	CustomerID      string                       `json:"customer_id,omitempty"`
	Method          string                       `json:"method"`
	Path            string                       `json:"path"`
	Signal          string                       `json:"signal"`
	Windows         []RouteMonitorEvidenceWindow `json:"windows"`
}
type RouteMonitorIncident struct {
	Version           int                    `json:"version"`
	ID                string                 `json:"id"`
	AppID             string                 `json:"app_id"`
	DeploymentID      string                 `json:"deployment_id"`
	Revision          int64                  `json:"revision"`
	Status            string                 `json:"status"`
	OpenedAt          time.Time              `json:"opened_at"`
	ClosedAt          *time.Time             `json:"closed_at,omitempty"`
	OpeningReport     RouteMonitorReport     `json:"opening_report"`
	RecoveryReport    *RouteMonitorReport    `json:"recovery_report,omitempty"`
	Evidence          []RouteMonitorEvidence `json:"evidence"`
	EvidenceTruncated bool                   `json:"evidence_truncated"`
}
type RouteMonitorIncidentPage struct {
	AppID      string                 `json:"app_id"`
	Incidents  []RouteMonitorIncident `json:"incidents"`
	NextBefore string                 `json:"next_before,omitempty"`
}

// Webhooks carry only metadata and an authenticated saved-incident path.
type RouteMonitorWebhookPayload struct {
	CustomerImpact *RouteMonitorCustomerImpact `json:"customer_impact,omitempty"`
	Version        int                         `json:"version"`
	AppID          string                      `json:"app_id"`
	DeploymentID   string                      `json:"deployment_id"`
	IncidentID     string                      `json:"incident_id"`
	Revision       int64                       `json:"revision"`
	Status         string                      `json:"status"`
	CheckedAt      time.Time                   `json:"checked_at"`
	IncidentPath   string                      `json:"incident_path"`
}

// Counts describe distinct recorded identities, not unique people or billing.
type RouteMonitorCustomerImpact struct {
	GroupBy           string `json:"group_by"`
	Coverage          string `json:"coverage"`
	ObservedCustomers int64  `json:"observed_customers"`
	ViolatedCustomers int64  `json:"violated_customers"`
}
type RouteMonitorCustomerWindow struct {
	Start                      time.Time `json:"start"`
	End                        time.Time `json:"end"`
	IdentifiedRequests         int64     `json:"identified_requests"`
	UnattributedRequests       int64     `json:"unattributed_requests"`
	UnresolvedIdentityRequests int64     `json:"unresolved_identity_requests"`
	OtherCustomerRequests      int64     `json:"other_customer_requests"`
}
type RouteMonitorCustomerCohort struct {
	CustomerID    string               `json:"customer_id,omitempty"`
	Observed      bool                 `json:"observed"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason"`
	ErrorStatus   string               `json:"error_status"`
	LatencyStatus string               `json:"latency_status"`
	Windows       []RouteMonitorWindow `json:"windows"`
}
type RouteMonitorCustomerRoute struct {
	Method                      string                       `json:"method"`
	Path                        string                       `json:"path"`
	ObservedCustomers           int64                        `json:"observed_customers"`
	ViolatedCustomers           int64                        `json:"violated_customers"`
	UnknownCustomers            int64                        `json:"unknown_customers"`
	RecoveryMissingCustomers    int64                        `json:"recovery_missing_customers"`
	RecoveryRemainingCustomers  int64                        `json:"recovery_remaining_customers"`
	CustomersTruncated          bool                         `json:"customers_truncated"`
	ViolatingCustomersTruncated bool                         `json:"violating_customers_truncated"`
	ViolatingCustomerIDs        []string                     `json:"violating_customer_ids,omitempty"`
	Windows                     []RouteMonitorCustomerWindow `json:"windows"`
	Customers                   []RouteMonitorCustomerCohort `json:"customers"`
}

// Full-population counts precede detail caps. Recovery checks keep previously
// violating identities in scope even when they disappear from current traffic.
type RouteMonitorCustomerReport struct {
	GroupBy                     string                      `json:"group_by"`
	DetailsIncluded             bool                        `json:"details_included"`
	Coverage                    string                      `json:"coverage"`
	Status                      string                      `json:"status"`
	Reason                      string                      `json:"reason"`
	CustomersLimit              int                         `json:"customers_limit"`
	ObservedCustomers           int64                       `json:"observed_customers"`
	ViolatedCustomers           int64                       `json:"violated_customers"`
	UnknownCustomers            int64                       `json:"unknown_customers"`
	RecoveryRemainingCustomers  int64                       `json:"recovery_remaining_customers"`
	RecoveryInventoryIncomplete bool                        `json:"recovery_inventory_incomplete"`
	Routes                      []RouteMonitorCustomerRoute `json:"routes"`
}

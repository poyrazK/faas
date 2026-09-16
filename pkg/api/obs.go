// Operator observability backend DTOs (issue #777 / ADR-091).
//
// Every type in this file is wire shape for /v1/admin/obs/* — the
// operator surface that lets the platform owner answer "do I have
// any users" and "what is happening" without per-account scope.
// The surface is operator-only (admin scope + FAAS_ADMIN_EMAILS
// allowlist) and deliberately excluded from the public OpenAPI
// spec; a frontend agent builds against the wire contract here
// rather than the SDK.
//
// Sensitive-field policy (ADR-091 §3, PII redaction by default):
//   - Email never appears on the default list/detail path.
//   - ?include_pii=1 is the only opt-in, and it emits a pii.accessed
//     audit row in the handler.
//   - No projection ever carries sealed-blob MFA fields, hashed
//     passwords, hashed tokens, sealed webhook secrets, sealed env
//     secrets, or jail internals (netns, guest_uid, host_ip,
//     lease_token). The handlers in cmd/apid/handlers_admin_obs.go
//     carry the projection helpers; the spec_compliance_test grep
//     tests pin the omissions in handlers_admin_obs_security_test.go.
//
// Pinned in ADR-091 §"Pagination caps are global constants": the
// default page size is 200, hard cap 500 (ObsAdminPaginationDefault /
// ObsAdminPaginationMax). The list responses always carry a
// non-nil Items slice so the JSON shape is stable.
package api

import (
	"encoding/json"
	"time"
)

// ObsOverviewResponse is the body of GET /v1/admin/obs/overview.
// A single-object response (not a list) because the overview is a
// KPI bundle, not a paginated collection. GeneratedAt is the
// server-side clock at handler entry; a frontend can display
// "snapshot at HH:MM" without parsing log timestamps.
type ObsOverviewResponse struct {
	GeneratedAt               time.Time                `json:"generated_at"`
	Totals                    ObsOverviewTotals        `json:"totals"`
	BetaFunnel14d             ObsBetaFunnel            `json:"beta_funnel_14d"`
	TopRateLimitedAccounts24h []ObsOverviewRateLimited `json:"top_rate_limited_accounts_24h"`
	NodeHealth                []ObsOverviewNodeHealth  `json:"node_health"`
	RecentFailures1h          []ObsOverviewFailureKind `json:"recent_failures_1h"`
}

// ObsBetaFunnel is the privacy-safe activation funnel for accounts created in
// the trailing 14-day window. Each stage describes the account's current
// progression and never exceeds the preceding stage. First-success latency is
// derived from the first successful public request persisted on an instance;
// no account IDs, emails, app slugs, or request metadata leave this aggregate.
type ObsBetaFunnel struct {
	WindowStartedAt                time.Time `json:"window_started_at"`
	AccountsCreated                int       `json:"accounts_created"`
	EmailVerified                  int       `json:"email_verified"`
	WithApp                        int       `json:"with_app"`
	WithLiveDeployment             int       `json:"with_live_deployment"`
	WithSuccessfulRequest          int       `json:"with_successful_request"`
	SignupToFirstSuccessSamples    int       `json:"signup_to_first_success_samples"`
	SignupToFirstSuccessP50Seconds int64     `json:"signup_to_first_success_p50_seconds"`
	SignupToFirstSuccessP95Seconds int64     `json:"signup_to_first_success_p95_seconds"`
}

// ObsOverviewTotals is the headline KPI block. The numbers are
// point-in-time counts derived from existing store methods; nothing
// here is sampled, weighted, or extrapolated. The cardinality is
// intentionally small (10 fields) so a frontend dashboard tile
// renders without scrolling.
type ObsOverviewTotals struct {
	AccountsActive    int `json:"accounts_active"`
	AccountsPastDue   int `json:"accounts_past_due"`
	AccountsSuspended int `json:"accounts_suspended"`
	OrgsTotal         int `json:"orgs_total"`
	AppsTotal         int `json:"apps_total"`
	InstancesLive     int `json:"instances_live"`
	InstancesWaking   int `json:"instances_waking"`
	NodesActive       int `json:"nodes_active"`
	NodesInactive     int `json:"nodes_inactive"`
	AuditEvents24h    int `json:"audit_events_24h"`
}

// ObsOverviewRateLimited is one row of the top-N rate-limited
// accounts tile on the overview. AccountID is a UUID string;
// Hits is the count over the 24h window. Cap of 5 entries is
// pinned in the handler (ADR-091 §"Label cardinality" — the top-N
// is bounded so a future Prometheus gauge can't blow up label
// cardinality if this view is scraped).
type ObsOverviewRateLimited struct {
	AccountID string `json:"account_id"`
	Hits      int    `json:"hits"`
}

// ObsOverviewNodeHealth is the per-node health summary on the
// overview. Stale is true when LastHeartbeatAt is older than the
// schedd staleness threshold (typically 60s); the operator UI
// renders it as a yellow chip so a node that's down shows
// visually distinct from one that's freshly heartbeating.
type ObsOverviewNodeHealth struct {
	Name            string    `json:"name"`
	Active          bool      `json:"active"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at,omitempty"`
	Stale           bool      `json:"stale"`
}

// ObsOverviewFailureKind is the failure-bucket rollup. The handler
// scans the audit_log table over a 1h window and groups by kind;
// anything past the top-5 (per ADR-091 §"Label cardinality")
// aggregates to a single "other" row.
type ObsOverviewFailureKind struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// ObsTenantRow is one row of the GET /v1/admin/obs/tenants
// response. PII redaction is enforced at the projection helper:
// Email is the empty string by default and populated only when
// the caller passed ?include_pii=1 (which also emits a
// pii.accessed audit row per ADR-091 §3).
//
// The *Count fields are pre-aggregated in the handler so the
// frontend does not need a second round-trip to render the
// dashboard tile. Cursor pagination is via NextCursor in the
// outer response.
type ObsTenantRow struct {
	AccountID            string    `json:"account_id"`
	Plan                 string    `json:"plan"`
	Status               string    `json:"status"`
	OrgSlug              string    `json:"org_slug,omitempty"`
	IsPersonal           bool      `json:"is_personal"`
	CreatedAt            time.Time `json:"created_at"`
	MFAEnrolled          bool      `json:"mfa_enrolled"`
	AppsCount            int       `json:"apps_count"`
	DeploymentsLiveCount int       `json:"deployments_live_count"`

	// Email is omitted on the default path. Wire-shape stays
	// consistent (a present-but-empty string) so a frontend can
	// branch on the field's presence; the projection helper
	// returns "" by default.
	Email string `json:"email,omitempty"`
}

// ObsTenantListResponse is the body of GET /v1/admin/obs/tenants.
// Cursor pagination follows the rest of the API (cmd/apid/
// handlers_account_scoped.go:54-82 pattern): NextCursor is the
// empty string when there are no more pages; Items is always
// non-nil.
type ObsTenantListResponse struct {
	Items      []ObsTenantRow `json:"items"`
	NextCursor string         `json:"next_cursor"`
	Limit      int            `json:"limit"`
}

// ObsTenantDetailResponse is the body of GET /v1/admin/obs/tenants/{id}.
// Account mirrors ObsTenantRow (minus the *_count fields, which
// move up to the top level for the detail view); Apps, Orgs,
// APIKeys, Sessions are per-tenant roll-ups so a single fetch
// renders the per-tenant drawer.
type ObsTenantDetailResponse struct {
	Account  ObsTenantRow    `json:"account"`
	Apps     []ObsTenantApp  `json:"apps"`
	Orgs     []ObsTenantOrg  `json:"orgs"`
	APIKeys  ObsTenantCounts `json:"api_keys"`
	Sessions ObsTenantCounts `json:"sessions"`
}

// ObsTenant360Response is the bounded operator view for one tenant. It keeps
// the existing identity/application projection and adds month-scoped usage
// plus a small billing window, so routine support work does not require a
// direct database query.
type ObsTenant360Response struct {
	Account  ObsTenantRow     `json:"account"`
	Apps     []ObsTenantApp   `json:"apps"`
	Orgs     []ObsTenantOrg   `json:"orgs"`
	APIKeys  ObsTenantCounts  `json:"api_keys"`
	Sessions ObsTenantCounts  `json:"sessions"`
	Usage    ObsTenantUsage   `json:"usage"`
	Billing  ObsTenantBilling `json:"billing"`
}

type ObsTenantUsage struct {
	Month           string              `json:"month"`
	UsedGBHours     float64             `json:"used_gb_hours"`
	IncludedGBHours int64               `json:"included_gb_hours"`
	OverageGBHours  float64             `json:"overage_gb_hours"`
	OverageCents    int64               `json:"overage_cents"`
	UsedCPUHours    float64             `json:"used_cpu_hours"`
	UsedEgressGB    float64             `json:"used_egress_gb"`
	UsedIngressGB   float64             `json:"used_ingress_gb"`
	ColdBootTotal   int64               `json:"cold_boots"`
	Requests        int64               `json:"requests"`
	Apps            []ObsTenantUsageApp `json:"apps"`
}

type ObsTenantUsageApp struct {
	AppID      string `json:"app_id"`
	AppSlug    string `json:"app_slug,omitempty"`
	MBSeconds  int64  `json:"mb_seconds"`
	CPUUsec    int64  `json:"cpu_usec"`
	Requests   int64  `json:"requests"`
	TXBytes    int64  `json:"tx_bytes"`
	NetTxBytes int64  `json:"net_tx_bytes"`
	NetRxBytes int64  `json:"net_rx_bytes"`
	ColdBoots  int64  `json:"cold_boots"`
}

type ObsTenantBilling struct {
	CurrentMonthOverageCents int64               `json:"current_month_overage_cents"`
	OverageCapCents          *int64              `json:"overage_cap_cents,omitempty"`
	ActiveCreditsCents       int64               `json:"active_credits_cents"`
	Invoices                 []ObsInvoiceSummary `json:"invoices"`
}

type ObsInvoiceSummary struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	Number          string    `json:"number,omitempty"`
	Status          string    `json:"status"`
	Currency        string    `json:"currency"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	TotalCents      int64     `json:"total_cents"`
	AmountPaidCents int64     `json:"amount_paid_cents"`
}

// ObsCapacityResponse is the bounded fleet capacity snapshot used by the
// operator capacity page. It contains resource headroom and placement
// counters, never workload rows.
type ObsCapacityResponse struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Summary     ObsCapacitySummary `json:"summary"`
	Nodes       []ObsCapacityNode  `json:"nodes"`
}

type ObsCapacitySummary struct {
	TotalNodes              int   `json:"total_nodes"`
	ActiveNodes             int   `json:"active_nodes"`
	InactiveNodes           int   `json:"inactive_nodes"`
	TotalVCPUs              int64 `json:"total_vcpus"`
	TotalVCPUBudget         int64 `json:"total_vcpu_budget"`
	TotalMemMB              int64 `json:"total_mem_mb"`
	TotalAdmissionCeilingMB int64 `json:"total_admission_ceiling_mb"`
	RAMUsedMB               int64 `json:"ram_used_mb"`
	AdmissionMarginMB       int64 `json:"admission_margin_mb"`
	InstancesLive           int64 `json:"instances_live"`
	InstancesRunning        int64 `json:"instances_running"`
	InstancesWaking         int64 `json:"instances_waking"`
	InstancesColdBooting    int64 `json:"instances_cold_booting"`
	AppsTotal               int64 `json:"apps_total"`
	TenantsTotal            int64 `json:"tenants_total"`
	UnplacedApps            int64 `json:"unplaced_apps"`
}

type ObsCapacityNode struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Active               bool   `json:"active"`
	VPCPUs               int    `json:"vpcpus"`
	VCPUBudget           int    `json:"vcpu_budget"`
	MemMB                int    `json:"mem_mb"`
	AdmissionCeilingMB   int    `json:"admission_ceiling_mb"`
	InstancesLive        int64  `json:"instances_live"`
	InstancesRunning     int64  `json:"instances_running"`
	InstancesWaking      int64  `json:"instances_waking"`
	InstancesColdBooting int64  `json:"instances_cold_booting"`
	RAMUsedMB            int64  `json:"ram_used_mb"`
	AdmissionMarginMB    int64  `json:"admission_margin_mb"`
	AppsCount            int64  `json:"apps_count"`
	TenantsCount         int64  `json:"tenants_count"`
}

// ObsTenantActivityResponse is the bounded activity view for one tenant.
// It intentionally carries invocation metadata and audit metadata only:
// request payloads, results, headers, and audit data may contain customer
// secrets and never belong in the operator activity feed.
type ObsTenantActivityResponse struct {
	AccountID   string                `json:"account_id"`
	GeneratedAt time.Time             `json:"generated_at"`
	Invocations []ObsInvocationRow    `json:"invocations"`
	AuditEvents []ObsAuditActivityRow `json:"audit_events"`
	Limit       int                   `json:"limit"`
}

type ObsAccountMutationResponse struct {
	Account         ObsTenantRow `json:"account"`
	Action          string       `json:"action"`
	RevokedSessions int          `json:"revoked_sessions"`
}

// ObsInvocationRow is the safe operational projection of an invocation.
type ObsInvocationRow struct {
	ID          string     `json:"id"`
	AppID       string     `json:"app_id"`
	AppSlug     string     `json:"app_slug,omitempty"`
	State       string     `json:"state"`
	Source      string     `json:"source"`
	Method      string     `json:"method"`
	Path        string     `json:"path"`
	Outcome     string     `json:"outcome,omitempty"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ObsAuditActivityRow is the safe metadata-only projection of audit_log.
// Data is deliberately omitted; the global audit page remains the explicit
// high-detail path for an operator who is authorized to inspect it.
type ObsAuditActivityRow struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Actor   string    `json:"actor,omitempty"`
	Subject string    `json:"subject,omitempty"`
}

// ObsAppDetailResponse is the workload drill-down used by operations to
// answer which deployment and instances belong to a customer app.
type ObsAppDetailResponse struct {
	App         ObsAppDetail       `json:"app"`
	Deployments []ObsDeploymentRow `json:"deployments"`
	Instances   []ObsInstanceRow   `json:"instances"`
	Invocations []ObsInvocationRow `json:"invocations"`
	Health      ObsAppHealth       `json:"health"`
}

type ObsAppHealth struct {
	GeneratedAt       time.Time             `json:"generated_at"`
	Metrics           AppMetricsResponse    `json:"metrics"`
	Errors            []AppErrorSummaryItem `json:"errors"`
	ErrorsWindowStart time.Time             `json:"errors_window_start"`
	ErrorsWindowEnd   time.Time             `json:"errors_window_end"`
}

type ObsAppDetail struct {
	ID             string    `json:"id"`
	AccountID      string    `json:"account_id"`
	Slug           string    `json:"slug"`
	Type           string    `json:"type"`
	Runtime        string    `json:"runtime"`
	Status         string    `json:"status"`
	RAMMB          int       `json:"ram_mb"`
	MaxConcurrency int       `json:"max_concurrency"`
	MinInstances   int       `json:"min_instances"`
	CreatedAt      time.Time `json:"created_at"`
}

type ObsDeploymentRow struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	Kind        string    `json:"kind"`
	ImageDigest string    `json:"image_digest,omitempty"`
	SourceURL   string    `json:"source_url,omitempty"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ObsInstanceRow struct {
	ID            string     `json:"id"`
	AppID         string     `json:"app_id"`
	AppSlug       string     `json:"app_slug,omitempty"`
	AccountID     string     `json:"account_id,omitempty"`
	DeploymentID  string     `json:"deployment_id"`
	NodeID        string     `json:"node_id,omitempty"`
	NodeName      string     `json:"node_name,omitempty"`
	State         string     `json:"state"`
	RAMMB         int        `json:"ram_mb"`
	StartedAt     time.Time  `json:"started_at"`
	LastRequestAt time.Time  `json:"last_request_at"`
	ParkedAt      *time.Time `json:"parked_at,omitempty"`
}

// ObsNodeDetailResponse is the node-to-workload drill-down. It is designed
// for bounded fleet inspection: apps are summarized and instances carry no
// jail internals, host IPs, or lease material.
type ObsNodeDetailResponse struct {
	Node      ObsNodeRow         `json:"node"`
	Apps      []ObsNodeApp       `json:"apps"`
	Instances []ObsInstanceRow   `json:"instances"`
	Drain     ObsNodeDrainStatus `json:"drain"`
}

type ObsNodeDrainStatus struct {
	TotalInstances   int       `json:"total_instances"`
	LiveInstances    int       `json:"live_instances"`
	RunningInstances int       `json:"running_instances"`
	WakingInstances  int       `json:"waking_instances"`
	ColdBooting      int       `json:"cold_booting_instances"`
	DrainSafe        bool      `json:"drain_safe"`
	ObservedAt       time.Time `json:"observed_at"`
}

type ObsNodeApp struct {
	ID                   string     `json:"id"`
	Slug                 string     `json:"slug"`
	AccountID            string     `json:"account_id"`
	Status               string     `json:"status"`
	InstancesLive        int        `json:"instances_live"`
	InstancesRunning     int        `json:"instances_running"`
	InstancesWaking      int        `json:"instances_waking"`
	InstancesColdBooting int        `json:"instances_cold_booting"`
	RAMUsedMB            int64      `json:"ram_used_mb"`
	LastRequestAt        *time.Time `json:"last_request_at,omitempty"`
}

type ObsNodeMutationResponse struct {
	OK                 bool                      `json:"ok"`
	IntentID           string                    `json:"intent_id"`
	StatusURL          string                    `json:"status_url"`
	ExpiresAt          time.Time                 `json:"expires_at"`
	Kind               string                    `json:"kind"`
	Node               string                    `json:"node"`
	PreviousActive     bool                      `json:"previous_active"`
	Active             bool                      `json:"active"`
	PreviousLifecycle  string                    `json:"previous_lifecycle"`
	RequestedLifecycle string                    `json:"requested_lifecycle"`
	LiveInstances      int                       `json:"live_instances"`
	Forced             bool                      `json:"forced"`
	Reason             string                    `json:"reason"`
	Preflight          ObsNodeOperationPreflight `json:"preflight"`
}

// ObsNodeOperationPreflight is the bounded impact summary persisted with a
// node lifecycle intent and returned to the operations console. It contains
// counts and capacity only; app, tenant, and instance identifiers remain on
// the authenticated node-detail read surface.
type ObsNodeOperationPreflight struct {
	AffectedApps      int   `json:"affected_apps"`
	AffectedTenants   int   `json:"affected_tenants"`
	TotalInstances    int   `json:"total_instances"`
	LiveInstances     int   `json:"live_instances"`
	LiveRAMMB         int64 `json:"live_ram_mb"`
	CapacityChangeMB  int   `json:"capacity_change_mb"`
	Reversible        bool  `json:"reversible"`
	DisruptionWarning bool  `json:"disruption_warning"`
}

// ObsTenantApp is a single app row in the tenant detail view.
// Status is the app state (active|evicted_cold|deleted_*) and
// Deployments is the count of non-deleted deployments. The
// handler pre-aggregates so the frontend renders the drawer
// without a second round-trip.
type ObsTenantApp struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Status      string `json:"status"`
	Deployments int    `json:"deployments"`
}

// ObsTenantOrg is a single org row in the tenant detail view.
// Role is the account's role in the org (owner|admin|developer|
// viewer|billing) per ADR-061.
type ObsTenantOrg struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

// ObsTenantCounts is the per-tenant counter pair. Used twice in
// ObsTenantDetailResponse (api_keys, sessions).
type ObsTenantCounts struct {
	Active  int `json:"active"`
	Revoked int `json:"revoked"`
}

// ObsNodeRow is one row of GET /v1/admin/obs/nodes. Mirrors the
// computeNodeResponse shape (cmd/apid/compute_nodes.go:107) so the
// existing operator tooling can switch over with no client
// changes; the only addition is LastHeartbeatAt omitempty for
// never-heartbeated nodes.
//
// PR #4 (ADR-092) extends this with 7 live-utilization fields:
// Instances{Live,Running,Waking,ColdBooting}, RAMUsedMB,
// AdmissionMarginMB (the §6.2 invariant #2 derivative),
// CPUPct60s, and DiskUsedBytes. The omitempty tags on the stat
// pointers mean a node that hasn't heartbeated since the
// migration won't render "0%" / "0 MB" — it renders "—".
type ObsNodeRow struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Active             bool      `json:"active"`
	VPCPUs             int       `json:"vpcpus"`
	MemMB              int       `json:"mem_mb"`
	MaxConcurrency     int       `json:"max_concurrency"`
	AdmissionCeilingMB int       `json:"admission_ceiling_mb"`
	OverlayIP          string    `json:"overlay_ip,omitempty"`
	LastHeartbeatAt    time.Time `json:"last_heartbeat_at,omitempty"`
	CreatedAt          time.Time `json:"created_at"`

	// PR #4 live utilization. InstancesLive counts rows in
	// {WAKING, COLD_BOOTING, RUNNING} on this node (§6.2
	// invariant #1 set); the per-state counters break that
	// total down so the operator UI can render "12 running /
	// 3 waking / 0 cold-booting" tiles.
	InstancesLive        int64 `json:"instances_live"`
	InstancesRunning     int64 `json:"instances_running"`
	InstancesWaking      int64 `json:"instances_waking"`
	InstancesColdBooting int64 `json:"instances_cold_booting"`
	// RAMUsedMB is the §6.2 invariant #2 number for this node:
	// Σ(ram_mb + 8) over live instances. The +8 is folded into
	// the SQL SUM and the memstore accumulator so the per-node
	// value sums to the fleet ceiling AdmissionCeilingMB.
	RAMUsedMB int64 `json:"ram_used_mb"`
	// AdmissionMarginMB is AdmissionCeilingMB - RAMUsedMB. A
	// negative value is rendered as a "near-ceiling" badge on
	// the operator UI; zero or positive is the headroom. Both
	// fields are int64 for the same reason RAMUsedMB is int64.
	AdmissionMarginMB int64 `json:"admission_margin_mb"`

	// PR #4 heartbeat stats (nullable; omitempty means pre-PR #4
	// heartbeats render as "—"). CPUPct60s is the 60-second
	// sliding-window CPU% bounded [0.00, 100.00]; DiskUsedBytes
	// is the byte size of /srv/fc/snapshots + spool scratchpad
	// at heartbeat-mint time.
	CPUPct60s     *float64 `json:"cpu_pct_60s,omitempty"`
	DiskUsedBytes *int64   `json:"disk_used_bytes,omitempty"`
}

// ObsNodeListResponse is the body of GET /v1/admin/obs/nodes.
// Cursor pagination is present on the obs surface even though
// /v1/compute-nodes today returns a flat array; the obs surface
// is the long-term canonical and the new contract is what the
// operator UI builds against.
type ObsNodeListResponse struct {
	Items      []ObsNodeRow `json:"items"`
	NextCursor string       `json:"next_cursor"`
	Limit      int          `json:"limit"`
}

// ObsHeartbeatListResponse is the body of GET /v1/admin/obs/nodes/{name}/heartbeats.
// The window defaults to 30m (handler), hard cap 24h
// (ObsAdminWindowMaxHours). SinceClamped is true when the
// caller passed a ?since= older than the hard cap and the
// handler clamped it; the frontend can show "clamped to 24h"
// so the operator knows they are not seeing the full history.
type ObsHeartbeatListResponse struct {
	NodeID       string            `json:"node_id"`
	Name         string            `json:"name"`
	Since        time.Time         `json:"since"`
	SinceClamped bool              `json:"since_clamped"`
	Heartbeats   []ObsHeartbeatRow `json:"heartbeats"`
	Limit        int               `json:"limit"`
}

// ObsWakeLatencyQuantile is one row of per-node wake-latency
// quantiles (PR #4 / ADR-092 §3.6). NodeID is the compute_nodes.id
// UUID; NodeName is the human-friendly name resolved by the
// handler via ListComputeNodes. P50MS / P95MS / P99MS are
// milliseconds. SampleCount is the underlying observation count
// (sum across the labelled histogram over the window) — a node
// with 0 samples renders as a row with all quantiles at 0 and
// the operator UI shows "no data".
type ObsWakeLatencyQuantile struct {
	NodeID      string  `json:"node_id"`
	NodeName    string  `json:"node_name"`
	P50MS       float64 `json:"p50_ms"`
	P95MS       float64 `json:"p95_ms"`
	P99MS       float64 `json:"p99_ms"`
	SampleCount int64   `json:"sample_count"`
}

// ObsNodeWakeLatencyResponse is the body of
// GET /v1/admin/obs/nodes/wake-latency. The window is fixed at
// 5m (matches the existing fleet wake_p95 cadence in
// handlers_account_scoped.go) so the operator can compare the
// per-node numbers to the fleet p95 in the same window.
type ObsNodeWakeLatencyResponse struct {
	Window     string                   `json:"window"`
	Quantiles  []ObsWakeLatencyQuantile `json:"quantiles"`
	FleetP95MS float64                  `json:"fleet_p95_ms"`
	AsOf       time.Time                `json:"as_of"`
}

// ObsHeartbeatRow is one row of the heartbeat list. The fields
// mirror the existing compute_node_heartbeats table; GapToPrevious
// is the millisecond gap from the prior row (handler-computed)
// so the operator can spot dropped heartbeats without scanning
// the raw timestamps.
type ObsHeartbeatRow struct {
	ReceivedAt      time.Time `json:"received_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	Source          string    `json:"source"`
	GapToPreviousMs int64     `json:"gap_to_previous_ms"`
	Missed          bool      `json:"missed"`
	Stale           bool      `json:"stale"`
}

// ObsAnomalyRow is one row of GET /v1/admin/obs/anomalies
// (ADR-091 §3.6 / PR #2). AccountID and AppID are UUID strings;
// the frontend can join to the tenant list for human rendering.
// BaselineMean / BaselineStddev / BaselineSamples are the values
// the SQL CTE used to score the row so the operator UI can show
// "150 vs mean 41 ± 8" without re-querying. ZScore is a
// float-pointer because very-low-traffic apps can have null
// scores (sample_count < 3) — those rows are pruned server-side
// and never reach the wire. Reason explains which detector fired:
// "hour_of_day" for the primary Z-score, "raw_z" for the low-
// traffic fallback (baseline_mean × 5 with stddev < 1.0).
//
// PR #4 (ADR-092 §3.4 amendment) adds NodeID + NodeName
// (omitempty): these are populated only when the operator
// passed ?group_by=node. The default ?group_by=app leaves them
// empty so existing dashboard renderers don't have to learn
// about the new field. NodeName is resolved server-side via
// the existing computeNodes slice in the handler.
type ObsAnomalyRow struct {
	AccountID       string   `json:"account_id"`
	AppID           string   `json:"app_id"`
	NodeID          string   `json:"node_id,omitempty"`
	NodeName        string   `json:"node_name,omitempty"`
	Minute          string   `json:"minute"`  // RFC 3339, parsed server-side
	Current         float64  `json:"current"` // mb_seconds in the minute
	BaselineMean    float64  `json:"baseline_mean"`
	BaselineStddev  float64  `json:"baseline_stddev"`
	BaselineSamples int      `json:"baseline_samples"`
	ZScore          *float64 `json:"z_score"`
	Reason          string   `json:"reason"`
}

// ObsAnomalyListResponse is the body of GET /v1/admin/obs/anomalies.
// GeneratedAt + WindowHours + BaselineWindowDays surface the query
// parameters the operator passed so the dashboard can render
// "scanned 24h vs 7d baseline" inline. GroupBy echoes the caller's
// ?group_by= parameter (default "app", opt-in "node") so the
// operator UI can confirm the grain. Items is always non-nil;
// an empty window returns an empty slice, not null.
type ObsAnomalyListResponse struct {
	GeneratedAt        time.Time       `json:"generated_at"`
	WindowHours        int             `json:"window_hours"`
	BaselineWindowDays int             `json:"baseline_window_days"`
	GroupBy            string          `json:"group_by"`
	Items              []ObsAnomalyRow `json:"items"`
}

// ObsRateLimitDurableRow is one row of the durable (Postgres)
// rate-limit aggregate (ADR-091 §3.5 / PR #2). AccountID is a
// UUID string; the all-zeros UUID represents the anonymous bucket
// (events.subject IS NULL → credential stuffing without a known
// account). LastEventAt is the timestamp of the most recent
// auth.rate_limited event for the bucket.
type ObsRateLimitDurableRow struct {
	AccountID   string    `json:"account_id"`
	Hits        int       `json:"hits"`
	LastEventAt time.Time `json:"last_event_at"`
}

// ObsRateLimitLiveRow is one row of the live (in-process limiter
// snapshot) view. The limiter is keyed by client IP alone, so
// the row carries IP — not account_id — and the operator UI
// surfaces the IP as the actionable signal. CurrentlyRateLimited
// is true when the IP's failure count is ≥ the bucket's
// MaxFailures (i.e. the limiter would 429 the next request from
// that IP). LiveHits30s is the failure count over the bucket
// window (default 1m, but the field name surfaces the practical
// "in the last 30s" semantics the operator cares about).
type ObsRateLimitLiveRow struct {
	IP                   string    `json:"ip"`
	CurrentlyRateLimited bool      `json:"currently_rate_limited"`
	LiveHits30s          int       `json:"live_hits_30s"`
	LastEventAt          time.Time `json:"last_event_at"`
}

// ObsRateLimitResponse is the body of GET /v1/admin/obs/rate-limits
// (ADR-091 §3.5 / PR #2). Sources is wire-stable: today always
// ["durable", "live"]; future additions (gatewayd-public rate-limit
// snapshot, etc.) appear here without breaking the contract.
// LagSeconds is the documented lag of the durable view (= the
// 30s aggregator flush + pg_notify round-trip). The operator UI
// renders it in a tooltip on the durable table.
type ObsRateLimitResponse struct {
	GeneratedAt time.Time                `json:"generated_at"`
	WindowHours int                      `json:"window_hours"`
	Sources     []string                 `json:"sources"`
	LagSeconds  int                      `json:"lag_seconds"`
	Durable     []ObsRateLimitDurableRow `json:"durable"`
	Live        []ObsRateLimitLiveRow    `json:"live"`
}

// ObsAuditLogSearchResponse is the body of GET /v1/admin/obs/audit-log/search
// (ADR-091 §3.7 / PR #3). Rows are projected verbatim from the FK-free
// audit_log table (migrations/00163) — the table has no GIN index on
// data and per ADR-091 §3.7.1 (amended 2026-08-10) the search surface
// does NOT offer free-text on data. Filters are ?account_id, ?kind_prefix,
// ?since, ?include_anonymous, ?limit; the full filter set is in
// pkg/state.AuditLogFilter. PII access is logged separately by the
// handler (every include_anonymous=true call emits a pii.accessed audit
// row keyed on the caller).
//
// Items is always non-nil so the JSON shape is stable on empty windows;
// IncludeAnonymous is the effective value (caller's request, not the
// server's default) so the operator UI can render "anonymous rows
// surfacing" without a second round-trip.
//
// ActorEmail / OperatorOnly / TargetAccountID echo the three new
// P4 filters (Commit 6 of the operator-side observability
// mega-PR) so the operator UI can re-render the filter chip
// strip without a second round-trip.
// NextBefore is an opaque compound cursor for the next older page;
// it is omitted when this page reaches the end.
type ObsAuditLogSearchResponse struct {
	GeneratedAt      time.Time        `json:"generated_at"`
	Items            []ObsAuditLogRow `json:"items"`
	Limit            int              `json:"limit"`
	IncludeAnonymous bool             `json:"include_anonymous"`
	WindowHours      int              `json:"window_hours"`
	KindPrefix       string           `json:"kind_prefix,omitempty"`
	AccountID        string           `json:"account_id,omitempty"`
	ActorEmail       string           `json:"actor_email,omitempty"`
	OperatorOnly     bool             `json:"operator_only,omitempty"`
	TargetAccountID  string           `json:"target_account_id,omitempty"`
	NextBefore       string           `json:"next_before,omitempty"`
}

// ObsAuditLogRow is one row of the audit-log search. The fields are
// the audit_log table verbatim — AccountID is the canonical UUID
// (empty for anonymous rows), AccountEmail is the copy-time capture
// (empty for anonymous rows), and Data is the verbatim JSON payload.
// The grep tests in handlers_admin_obs_pr3_security_test.go pin the
// omission of any caller-side redaction; this struct IS the wire shape.
//
// IsOperatorAction is a derived field (Commit 6 / P4) — true
// when Kind has the "operator.action." prefix (the operator
// action vocabulary adopted in Commit 3). Computed at the
// handler projection so the operator dashboard can badge the
// row without re-deriving the prefix on every render.
type ObsAuditLogRow struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	AccountID        string          `json:"account_id,omitempty"`
	AccountEmail     string          `json:"account_email,omitempty"`
	Actor            string          `json:"actor,omitempty"`
	ReceivedAt       time.Time       `json:"received_at"`
	Data             json.RawMessage `json:"data,omitempty"`
	IsOperatorAction bool            `json:"is_operator_action"`
}

// ObsEventListResponse is the body of GET /v1/admin/obs/events
// (ADR-091 §3.7 / PR #3). Distinct from ObsAuditLogSearchResponse
// in two load-bearing axes (ADR-091 §3.7.4, "one source of truth per
// intent"):
//
//   - Source table: events (live, bigint id, append-only) vs
//     audit_log (FK-free, copy-time evidence). The two surfaces do
//     NOT overlap.
//   - Filter set: events has no `include_anonymous` toggle (every
//     events row has an actor) and surfaces `actor` / `subject` /
//     `kind_prefix` instead of `account_id` / `kind_prefix`.
//
// Items is always non-nil so the JSON shape is stable on empty windows.
type ObsEventListResponse struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Items       []ObsEventRow `json:"items"`
	Limit       int           `json:"limit"`
	WindowHours int           `json:"window_hours"`
	KindPrefix  string        `json:"kind_prefix,omitempty"`
	Actor       string        `json:"actor,omitempty"`
	Subject     string        `json:"subject,omitempty"`
}

// ObsEventRow is one row of the events table read. The bigint id
// surfaces as a string so JavaScript clients don't lose precision;
// At is the event timestamp (RFC 3339 wire format). Subject is the
// optional UUID the event relates to (e.g. an app_id for wake.* events).
// Data is the verbatim JSON payload — admins need to see wake_id,
// sidecar_name, instance_id, etc.
//
// PR #3 deliberately does NOT redact Data; the operator surface is
// admin-only and the data column is the source of truth for the
// related wire payloads (DEC-2 / ADR-091 §3.7.4).
type ObsEventRow struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor"`
	Kind    string          `json:"kind"`
	Subject string          `json:"subject,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ObsTraceLookupResponse is the bounded diagnostic view for one exact OTel
// trace id. It correlates durable operator intents with their live events
// without requiring operators to query either table directly.
type ObsTraceLookupResponse struct {
	TraceID     string                   `json:"trace_id"`
	GeneratedAt time.Time                `json:"generated_at"`
	Limit       int                      `json:"limit"`
	Intents     []OperatorIntentResponse `json:"intents"`
	Events      []ObsEventRow            `json:"events"`
}

// ObsBuilderHeartbeatListResponse is the response of the
// /v1/admin/obs/builder-heartbeats endpoint (operator-side
// observability mega-PR / Commit 7 — P5). One row per active
// builderd that has stamped a `builder_tick` heartbeat recently;
// QueuedBuilds is the fleet-total in-flight build queue.
//
// Both fields are always non-nil so the JSON shape is stable on
// empty / quiet cases — a fresh cluster with no builderds emits
// `{"items":[],"queued_builds":0}`.
type ObsBuilderHeartbeatListResponse struct {
	GeneratedAt  time.Time                `json:"generated_at"`
	Items        []ObsBuilderHeartbeatRow `json:"items"`
	QueuedBuilds int                      `json:"queued_builds"`
}

// ObsBuilderHeartbeatRow is one row of the builderd heartbeat
// projection. Mirrors ObsHeartbeatRow but for the
// source='builder_tick' slice. The builderd writer emits the
// source and timestamps; CPUPct60s + DiskUsedBytes mirror the vmmd
// heartbeat columns; nullable because the builderd writer
// is not yet emitting them.
type ObsBuilderHeartbeatRow struct {
	NodeID        string    `json:"node_id"`
	ReceivedAt    time.Time `json:"received_at"`
	CPUPct60s     *float64  `json:"cpu_pct_60s,omitempty"`
	DiskUsedBytes *int64    `json:"disk_used_bytes,omitempty"`
}

// ObsHealthResponse (Obs-Meta + Trace-IDs Mega-PR / C7) is the
// body of GET /v1/admin/obs/health. The endpoint exists so the
// operator can answer "is the obs stack itself healthy?" — a
// meta-question about the audit / operator-action pipeline,
// not the customer-facing fleet.
//
// Field shape (closed-set):
//
//   - AuditLogWriteTotal5m / AuditLogWriteFailures5m: counter
//     deltas over a 5m window from the apid Prometheus
//     (`audit_log_write_total` / `audit_log_write_failures_total`,
//     PR #TBD / C5).
//   - AuditLogCoverageRatio5m: ratio of audit_log writes with a
//     non-NULL trace_id over all audit_log writes in the window.
//     Surfaces the same trace_id completeness metric from the
//     C5 audit metric set, scoped to all kinds.
//   - OperatorIntentOutcomeMissingCounts: stuck-running intents
//     grouped by kind (force_park / force_cold_boot /
//     force_restart). When the count is non-zero the operator
//     action pipeline is degraded; the dashboard surfaces this
//     as a red tile.
//   - TraceIDCompletenessRatio: per-kind ratio of operator.action.*
//     events with a non-NULL trace_id over all operator.action.*
//     events in the window. Kinds with zero rows are seeded to
//     1.0 (vacuous truth — see Store interface comment).
//   - AlertsFiring: count of Prometheus alert rules in the firing
//     state via PromQL ALERTS{alertstate="firing"} (existing
//     Alertmanager integration).
//
// Prometheus-derived fields retain stable numeric fallbacks for compatibility,
// but PrometheusAvailable explicitly tells consumers whether those values came
// from a live query. SQL-derived fields still fail the endpoint when the local
// source of truth is unavailable.
type ObsHealthResponse struct {
	GeneratedAt                        time.Time          `json:"generated_at"`
	PrometheusAvailable                bool               `json:"prometheus_available"`
	AuditLogWriteTotal5m               int64              `json:"audit_log_write_total_5m"`
	AuditLogWriteFailures5m            int64              `json:"audit_log_write_failures_5m"`
	AuditLogCoverageRatio5m            float64            `json:"audit_log_coverage_ratio_5m"`
	OperatorIntentOutcomeMissingCounts map[string]int     `json:"operator_intent_outcome_missing_total"`
	TraceIDCompletenessRatio           map[string]float64 `json:"trace_id_completeness_ratio"`
	AlertsFiring                       int64              `json:"alerts_firing"`
}

// ObsHealthKindVocabulary is the closed set of operator-action
// kinds the /v1/admin/obs/health handler emits to
// OperatorIntentOutcomeMissingCounts + TraceIDCompletenessRatio.
// The handler seeds zero-count kinds with 0 (counts) or 1.0
// (ratios) so the JSON shape is stable. Adding a new kind means
// appending to this set AND emitting an audit row of the new
// kind from the matching handler.
//
// Exported so cmd/apid (obs_health_query.go) and the test files
// can reference the closed-set seed without copy-paste.
var ObsHealthKindVocabulary = []string{
	"force_park",
	"force_cold_boot",
	"force_restart",
	"node_drain",
	"node_force_drain",
	"node_activate",
}

// ObsIncident is one row of the bounded operator incident inbox. It is a
// correlation projection over existing deployment, job-run, compute-node,
// and Prometheus alert signals; it is not a second source of truth. Resource
// identifiers are safe operational handles, while customer inputs, image
// references, environment values, and raw log paths stay out of the shape.
type ObsIncident struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Severity     string    `json:"severity"`
	Status       string    `json:"status"`
	Summary      string    `json:"summary"`
	AccountID    string    `json:"account_id,omitempty"`
	AppID        string    `json:"app_id,omitempty"`
	ResourceID   string    `json:"resource_id"`
	ResourceName string    `json:"resource_name,omitempty"`
	ObservedAt   time.Time `json:"observed_at"`
	DedupeKey    string    `json:"dedupe_key"`
	ActionPath   string    `json:"action_path,omitempty"`
	AuditPath    string    `json:"audit_path,omitempty"`
	RunbookURL   string    `json:"runbook_url,omitempty"`
}

// ObsIncidentListResponse is the body of GET /v1/admin/obs/incidents. The
// response is cursor-paginated after all source signals are merged and
// deterministically ordered by observed_at DESC, id DESC. Items is always a
// non-nil slice so an empty fleet has a stable JSON shape.
type ObsIncidentListResponse struct {
	GeneratedAt  time.Time     `json:"generated_at"`
	Items        []ObsIncident `json:"items"`
	Limit        int           `json:"limit"`
	Since        time.Time     `json:"since"`
	SinceClamped bool          `json:"since_clamped"`
	Type         string        `json:"type,omitempty"`
	Severity     string        `json:"severity,omitempty"`
	NextCursor   string        `json:"next_cursor"`
}

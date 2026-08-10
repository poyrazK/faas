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
	TopRateLimitedAccounts24h []ObsOverviewRateLimited `json:"top_rate_limited_accounts_24h"`
	NodeHealth                []ObsOverviewNodeHealth  `json:"node_health"`
	RecentFailures1h          []ObsOverviewFailureKind `json:"recent_failures_1h"`
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
type ObsAnomalyRow struct {
	AccountID       string   `json:"account_id"`
	AppID           string   `json:"app_id"`
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
// "scanned 24h vs 7d baseline" inline. Items is always non-nil;
// an empty window returns an empty slice, not null.
type ObsAnomalyListResponse struct {
	GeneratedAt        time.Time       `json:"generated_at"`
	WindowHours        int             `json:"window_hours"`
	BaselineWindowDays int             `json:"baseline_window_days"`
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
type ObsAuditLogSearchResponse struct {
	GeneratedAt      time.Time        `json:"generated_at"`
	Items            []ObsAuditLogRow `json:"items"`
	Limit            int              `json:"limit"`
	IncludeAnonymous bool             `json:"include_anonymous"`
	WindowHours      int              `json:"window_hours"`
	KindPrefix       string           `json:"kind_prefix,omitempty"`
	AccountID        string           `json:"account_id,omitempty"`
}

// ObsAuditLogRow is one row of the audit-log search. The fields are
// the audit_log table verbatim — AccountID is the canonical UUID
// (empty for anonymous rows), AccountEmail is the copy-time capture
// (empty for anonymous rows), and Data is the verbatim JSON payload.
// The grep tests in handlers_admin_obs_pr3_security_test.go pin the
// omission of any caller-side redaction; this struct IS the wire shape.
type ObsAuditLogRow struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	AccountID    string          `json:"account_id,omitempty"`
	AccountEmail string          `json:"account_email,omitempty"`
	Actor        string          `json:"actor,omitempty"`
	ReceivedAt   time.Time       `json:"received_at"`
	Data         json.RawMessage `json:"data,omitempty"`
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

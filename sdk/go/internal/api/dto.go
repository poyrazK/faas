package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// Wire DTOs for the v1 REST API (spec Appendix A). Defined once here so apid and
// the faas CLI share exactly one contract; `--json` output stability (UX §3.2)
// depends on these shapes.

// ResourceProfile is a named RAM/CPU shape supported by the public API.
type ResourceProfile string

const (
	ResourceProfileMicro  ResourceProfile = "micro"
	ResourceProfileSmall  ResourceProfile = "small"
	ResourceProfileMedium ResourceProfile = "medium"
	ResourceProfileLarge  ResourceProfile = "large"
	ResourceProfileXLarge ResourceProfile = "xlarge"
)

type ResourceProfileSpec struct {
	Name          ResourceProfile
	MemoryMB      int
	CPUMillicores int
}

// AppRestartResponse is returned when a customer restart request is accepted.
type AppRestartResponse struct {
	WakeID string `json:"wake_id"`
}

// CreateAppRequest creates an app or function.
type CreateAppRequest struct {
	Slug            string `json:"slug"`
	Type            string `json:"type,omitempty"`    // "app" (default) | "function"
	Runtime         string `json:"runtime,omitempty"` // node22|python312|go124|go124-alpine for functions
	RAMMB           int    `json:"ram_mb,omitempty"`  // 0 => plan default
	CPUMillicores   int    `json:"cpu_millicores,omitempty"`
	ResourceProfile string `json:"resource_profile,omitempty"`
	MaxConcurrency  int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS    int    `json:"idle_timeout_s,omitempty"`
	// Lifecycle fields are optional at create-time. Empty values preserve the
	// request-driven default; service_replicas is valid only for service mode.
	ExecutionMode    string           `json:"execution_mode,omitempty"`
	RestartPolicy    string           `json:"restart_policy,omitempty"`
	StartupDeadlineS int              `json:"startup_deadline_s,omitempty"`
	MaxRetries       int              `json:"max_retries,omitempty"`
	ServiceReplicas  *ServiceReplicas `json:"service_replicas,omitempty"`
	// OverflowNode (Tier A10 / ADR-088) is the customer's per-app
	// preferred spill target. The wire form is a
	// compute_nodes.name (the operator-supplied human-readable
	// label), NOT a UUID — apid resolves the name to the
	// underlying compute_nodes.id server-side. nil → no
	// preference (the engine's A9 first-peer-with-headroom
	// fallback). The string-shape rejects a silent "" at
	// create-time (the column starts NULL), so callers that
	// mean "no preference" should leave the field nil.
	OverflowNode *string `json:"overflow_node,omitempty"`
}

// UpdateAppRequest is the partial-update payload for PATCH /v1/apps/{slug}.
// All fields are pointers so the wire form can distinguish "not set" from
// "set to zero".
type UpdateAppRequest struct {
	RAMMB           *int    `json:"ram_mb,omitempty"`
	CPUMillicores   *int    `json:"cpu_millicores,omitempty"`
	ResourceProfile *string `json:"resource_profile,omitempty"`
	IdleTimeoutS    *int    `json:"idle_timeout_s,omitempty"`
	MaxConcurrency  *int    `json:"max_concurrency,omitempty"`
	// Lifecycle fields are tri-state: nil leaves the current value unchanged.
	// service_replicas replaces the complete replica policy when present.
	ExecutionMode    *string          `json:"execution_mode,omitempty"`
	RestartPolicy    *string          `json:"restart_policy,omitempty"`
	StartupDeadlineS *int             `json:"startup_deadline_s,omitempty"`
	MaxRetries       *int             `json:"max_retries,omitempty"`
	ServiceReplicas  *ServiceReplicas `json:"service_replicas,omitempty"`
	// MinInstances is the per-app cold-wake floor (ux_spec §6.5).
	// 0 / unset => scale to zero; >0 => keep at least this many
	// RUNNING instances alive. Pro/Scale only — Free/Hobby get
	// 403 plan_min_instances_not_allowed (apid gate). Must be <=
	// plan MaxConcurrency (422 invalid_min_instances).
	MinInstances *int `json:"min_instances,omitempty"`
	// EgressAllowlist (ADR-031 + ADR-032, tier-2 of the network
	// roadmap) is the per-app outbound IP allowlist. Each entry is
	// a CIDR string ("1.2.3.0/24" for v4, "2001:db8::/32" for v6);
	// the slice replaces the full list (atomic full-overwrite at the
	// apps row). Plan-gated upstream (Free/Hobby return 403
	// plan_egress_allowlist_not_allowed); size-capped at
	// plan.EgressAllowlistMaxSize() (Pro 16, Scale 64) — v4 + v6
	// entries share the same count budget. Empty slice / nil
	// pointer = clear the allowlist (back to the default-accept
	// chain policy). The non-/0 contract is enforced by the DB
	// trigger `apps_egress_allowlist_cidr` (migration 00033).
	EgressAllowlist *[]string `json:"egress_allowlist,omitempty"`
	// EvictionPriority (issue #475) is the per-app eviction tier
	// classification. Values: 'best_effort' (default, pre-#475
	// behaviour) or 'reserved' (opt-in protected tier). The plan
	// gate is enforced server-side — Free PATCH 'reserved' returns
	// 402 plan_eviction_priority_reserved_not_allowed. The
	// per-account cap (Hobby 1, Pro 2, Scale 4) returns 422
	// plan_eviction_priority_reserved_quota. nil → keep current
	// value. Use Client.SetAppEvictionPriority for the common
	// one-liner; this field is exposed for callers that bundle
	// eviction_priority into a wider PATCH.
	EvictionPriority *string `json:"eviction_priority,omitempty"`
	// OverflowNode (Tier A10 / ADR-088) is the customer's per-app
	// preferred spill target. The wire form is a
	// compute_nodes.name; apid resolves to a UUID server-side.
	// Tri-state: nil → don't touch the column; "" → clear the
	// preference; non-empty → resolve + set. Same surface
	// contract as EvictionPriority (pointer-to-string with
	// omitempty). Use Client.SetAppOverflowNode for the common
	// one-liner.
	OverflowNode *string `json:"overflow_node,omitempty"`
	// AutoscaleTargetRPS is the per-instance RPS target for the
	// reactive scale-up trigger (issue #169 / #172 / pkg/sched/scaleup).
	// When measured RPS / live_instance_count exceeds this value,
	// schedd admits another instance up to plan.MaxConcurrency. Plan-gated
	// upstream: Free returns 403 CodePlanScaleUpNotAllowed. Hobby/Pro/Scale
	// accept values > 0; values <= 0 return 422 CodeInvalidAutoscaleTargetRPS.
	// Autoscale is "enabled" iff at least one of AutoscaleTargetRPS /
	// AutoscaleTargetCPUPct is non-nil (no separate boolean, per user
	// direction).
	AutoscaleTargetRPS *int `json:"autoscale_target_rps,omitempty"`
	// AutoscaleTargetCPUPct is the per-instance CPU% target (1..100)
	// for the scale-up trigger. Same semantics as AutoscaleTargetRPS
	// but the signal source is pkg/sched/instancestats.Reader (PR #205);
	// nil reader falls back to RPS-only mode (PR #169 never lands the
	// CPU path). Pro/Scale only; Free/Hobby return 403 CodePlanScaleUpNotAllowed.
	// Values outside [1, 100] return 422 CodeInvalidAutoscaleTargetCPUPct.
	AutoscaleTargetCPUPct *int `json:"autoscale_target_cpu_pct,omitempty"`
	// PublicAuth (issue #477 / ADR-079) is the per-app
	// public-URL auth configuration write shape. nil →
	// leave the column untouched (the apid path's
	// SetPublicAuth=false semantics). When present, Mode
	// is the closed enum {open, bearer, basic}; BasicUser
	// + BasicPass are PLAINTEXT at PATCH time — the apid
	// seal step encrypts them under the APP_BASIC_AUTH
	// secretbox namespace before persistence. Both
	// fields are required iff Mode='basic'. Plan-gated
	// upstream: Free + bearer/basic = 402
	// plan_public_auth_*_not_allowed (Free/Hobby + basic =
	// 402; Hobby + bearer = ok). Use
	// Client.SetAppPublicAuth for the common one-liner;
	// this field is exposed for callers that bundle
	// public_auth into a wider PATCH.
	PublicAuth *PublicAuthBlock `json:"public_auth,omitempty"`
}

// RenameAppRequest is the body of POST /v1/apps/{slug}/rename (issue #63).
// Validated server-side via the same validSlug regex used at CreateApp
// time; rejected on conflict with 409 CodeAppRenameFailed when another
// live app already holds NewSlug.
type RenameAppRequest struct {
	NewSlug string `json:"new_slug"`
}

// AppEffectiveLimits is the effective resource and request envelope for an app.
type AppEffectiveLimits struct {
	MemoryLimitMB          int   `json:"memory_limit_mb"`
	PlanMemoryMaxMB        int   `json:"plan_memory_max_mb"`
	EphemeralDiskMaxMB     int   `json:"ephemeral_disk_max_mb"`
	GuestVCPUs             int   `json:"guest_vcpus"`
	CPULimitMillicores     int   `json:"cpu_limit_millicores"`
	PlanCPUMaxMillicores   int   `json:"plan_cpu_max_millicores"`
	CPUWeight              int   `json:"cpu_weight"`
	MaxInstances           int   `json:"max_instances"`
	ConcurrencyPerInstance int   `json:"concurrency_per_instance"`
	AppRequestRateRPS      int   `json:"app_request_rate_rps"`
	AppRequestBurst        int   `json:"app_request_burst"`
	AccountRequestRateRPM  int   `json:"account_request_rate_rpm"`
	RequestBudgetMS        int64 `json:"request_budget_ms"`
	RequestBudgetMaxMS     int64 `json:"request_budget_max_ms"`
	ResponseWriteTimeoutS  int64 `json:"response_write_timeout_s"`
}

type AppConfiguredResources struct {
	MemoryMB      int `json:"memory_mb"`
	CPUMillicores int `json:"cpu_millicores"`
}

// AppResponse is an app as returned by the API.
type AppResponse struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Type            string `json:"type"`
	Runtime         string `json:"runtime,omitempty"`
	RAMMB           int    `json:"ram_mb"`
	CPUMillicores   int    `json:"cpu_millicores"`
	ResourceProfile string `json:"resource_profile,omitempty"`
	MaxConcurrency  int    `json:"max_concurrency"`
	// ConcurrencyPerVMBound (issue #559) is the platform-advertised
	// per-VM concurrency cap for the customer's plan. Distinct from
	// MaxConcurrency (the per-app instance cap, spec §6.2-1) — this
	// is per-VM. Free 1, Hobby 5, Pro 25, Scale 80. Surfaced so
	// dashboards / CLI can show "what's the bound for one VM on
	// this plan" without the customer reading limits.go. Concurrency
	// above 1 is the customer's runner/process responsibility — see
	// spec §4.9 for the per-runtime concurrency model (Node
	// single-event-loop, Python asyncio, Go net/http are
	// concurrency-safe; sync subprocess-per-request handlers are
	// not).
	ConcurrencyPerVMBound int                    `json:"concurrency_per_vm"`
	EffectiveLimits       AppEffectiveLimits     `json:"effective_limits"`
	ConfiguredResources   AppConfiguredResources `json:"configured_resources"`
	// RequireAuthn (issue #560) is the per-deployment token-
	// gate flag. When true, every incoming request to this
	// app must carry a valid `Authorization: Bearer <token>`
	// header whose key belongs to the app's owning account
	// (cross-account tokens receive 403 insufficient_scope
	// at gatewayd-internal). Pro/Scale only — Free/Hobby
	// PATCH-true is rejected with 403
	// plan_require_authn_not_allowed at apid. Default false
	// at create-time (preserves the existing
	// public-by-default behaviour). Surfaced so the dashboard
	// can render a "Token-gated" badge without a second
	// round-trip; the SDK's WithToken() helper (client.go:110)
	// already injects the bearer header on every request,
	// so a customer calling CreateApp with RequireAuthn=true
	// on the CLI just needs the token set on the client.
	RequireAuthn bool `json:"require_authn"`
	// PublicAuth (issue #477 / ADR-079) is the per-app
	// public-URL auth configuration. Mode is the closed
	// enum {open, bearer, basic}; HasBasicCreds is true
	// iff the row carries a sealed APP_BASIC_AUTH blob.
	// The plaintext basic_user / basic_pass are NEVER
	// returned on this surface — the apid redaction
	// posture is a load-bearing invariant (see
	// ADR-079 §Decision "re-redaction invariant"). To
	// rotate credentials, the customer PATCHes a fresh
	// public_auth block.
	PublicAuth   *PublicAuthStatus `json:"public_auth,omitempty"`
	IdleTimeoutS int               `json:"idle_timeout_s,omitempty"`
	// MinInstances is the per-app cold-wake floor (ux_spec §6.5).
	// 0 => scale to zero; >0 => keep N warm. Pro/Scale only.
	MinInstances int    `json:"min_instances"`
	Status       string `json:"status"`
	URL          string `json:"url"`
	// Manifest is the runner-scaffold payload (env, healthz path,
	// entrypoint). Surfaced so the dashboard's app detail page can
	// show the function handler + env without a separate round-trip.
	// The DTO reuses the existing api.AppManifest (defined in
	// appmanifest.go) so the wire shape stays a single source of truth.
	Manifest AppManifest `json:"manifest"`
	// EgressAllowlist (ADR-031 + ADR-032, tier-2 of the network
	// roadmap) is the per-app outbound CIDR allowlist. Each entry
	// is the canonical CIDR string form: v4 ("1.2.3.0/24") or v6
	// ("2001:db8::/32"). The v4-mapped v6 form ("::ffff:1.2.3.0/120")
	// is silently rewritten to its v4 form at PATCH time by
	// validateUpdateApp, so the read-back never carries a
	// "::ffff:" prefix. Materialised as `[]` (never `null`) at
	// the conversion boundary (cmd/apid/handlers.go::appResponse)
	// so Free / Hobby and pre-PATCH apps always have a predictable
	// JSON shape — the per-netns renderer treats the empty list as
	// "no allowlist rule" (the chain falls back to default-accept).
	// The list is first-seen-wins-dedup'd at write time; the read
	// order matches insertion order. NOT in `required:` because the
	// empty-slice case is the contract.
	EgressAllowlist []string `json:"egress_allowlist"`
	// AutoscaleTargetRPS / AutoscaleTargetCPUPct are the per-app
	// reactive scale-up targets (issue #169 / #172 / pkg/sched/scaleup).
	// Each is 0 when unset ("disabled") and > 0 when configured.
	// Surfaces on GET /v1/apps/{slug} so dashboards can show the
	// current target. Plan-gated upstream.
	AutoscaleTargetRPS    int `json:"autoscale_target_rps"`
	AutoscaleTargetCPUPct int `json:"autoscale_target_cpu_pct"`
	// EvictionPriority (issue #475) is the per-app eviction tier
	// classification. 'best_effort' (default for every pre-#475
	// row, applied by the column DEFAULT at migration time) keeps
	// the historical LRU-by-last_request_at reaper behaviour;
	// 'reserved' (Hobby+ only, per-account cap enforced) protects
	// the app from cross-account RAM-pressure eviction.
	EvictionPriority string `json:"eviction_priority"`
	// OverflowNode (Tier A10 / ADR-088) is the resolved UUID of
	// the customer's per-app preferred spill target for the
	// pressure-rebalance path (pkg/sched/engine.RebalancePressuredApps).
	// nil when no preference is set — the engine falls back to
	// the A9 first-peer-with-headroom path. The wire is a UUID
	// string (the server resolved the customer-supplied
	// compute_nodes.name at create / PATCH time, so the value
	// on this surface is unambiguous across operator-deployed
	// fleets).
	OverflowNode *string `json:"overflow_node,omitempty"`
}

// CreateDeploymentRequest ships a version (JSON variant; the multipart
// variant is used for tarball/dockerfile deploys). TrafficPercent is
// the canary weight (issue #556 PR-A); nil = server default 100,
// explicit 0..100 = opt into split (Pro/Scale only).
type CreateDeploymentRequest struct {
	Image          string `json:"image,omitempty"` // registry.gregale.dev/...@sha256:...
	TrafficPercent *int   `json:"traffic_percent,omitempty"`
}

// DeploymentResponse is a deployment as returned by the API.
type DeploymentResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	BuildID     string `json:"build_id,omitempty"`
	ImageDigest string `json:"image_digest"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	// ErrorCode carries the RFC 7807 code ADR-021 lifted from the
	// puller-side sentinels (image_not_found / image_egress_denied /
	// image_manifest_invalid). Empty for every deployment created
	// before migrations/00021 OR that is not in a failure state —
	// api/state.SerializeDeployment knows the column is a string and
	// that "" is the canonical empty value, so the dashboard /
	// programmatic consumer can branch on ErrorCode != "".
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorHint / ErrorWhy / ErrorFix are the customer-facing
	// explanation prose (spec §6.4 amendment 1) stamped alongside
	// ErrorCode. Mirrors the wire-side Problem.Hint / Why / Fix
	// fields so third-party Go SDK consumers see the same 3-5 line
	// shape that the deploy-time Problem emits. Empty for
	// deployments created before migrations/00290 OR that are not
	// in a failure state — callers branch on the same
	// ErrorCode != "" test and render the four together.
	ErrorHint string `json:"error_hint,omitempty"`
	ErrorWhy  string `json:"error_why,omitempty"`
	ErrorFix  string `json:"error_fix,omitempty"`
	// ErrorRelevantLogs is the last N log lines that explain the
	// failure, surfaced inline when the deployment row carries
	// them. Capped at 20 entries × 512 bytes each (CLI tripwire;
	// see pkg/whycopy.Render for the catalogue row).
	ErrorRelevantLogs []LogExcerpt `json:"error_relevant_logs,omitempty"`
	CreatedAt         string       `json:"created_at"`
	SourceRoot        string       `json:"source_root,omitempty"`
	TrafficPercent    int          `json:"traffic_percent,omitempty"`
}

// UpdateDeploymentTrafficRequest is the body for
// PATCH /v1/deployments/{id}/traffic (issue #556 PR-A). Required
// field — handler rejects an absent body at 400 before the plan
// gate (403) and range check (422).
type UpdateDeploymentTrafficRequest struct {
	TrafficPercent int `json:"traffic_percent"`
}

// AccountResponse is the whoami payload. Limits is the plan's
// quota/limit table (RAM MB, max concurrency, included GB-h,
// deployed-app cap) so the dashboard /account page can show
// "you have X of Y apps" without a second round trip. UsageGBHours
// is the roll-up for the current month (caller-aggregated from
// Store.UsageByHour in apid; included here so the dashboard can
// render the meter in one fetch).
type AccountResponse struct {
	ID                string        `json:"id"`
	Email             string        `json:"email"`
	Plan              string        `json:"plan"`
	Status            string        `json:"status"`
	Limits            AccountLimits `json:"limits"`
	UsageGBHours      float64       `json:"usage_gb_hours"`
	AppCount          int           `json:"app_count"`
	DeveloperAppCount int           `json:"developer_app_count"`
	GitHubInstall     string        `json:"github_install_id,omitempty"`
	PlanChangeStatus  string        `json:"plan_change_status,omitempty"`
	RequestedPlan     string        `json:"requested_plan,omitempty"`
	EffectiveAt       *time.Time    `json:"effective_at,omitempty"`
}

// AccountLimits is the read-only copy of api.Limits that survives
// serialization. Stripped of fields the dashboard doesn't need
// (eg. internal ops); mirror pkg/api/limits.go for the wiring.
type AccountLimits struct {
	Plan               string `json:"plan"`
	RAMMB              int    `json:"ram_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	DeployedApps       int    `json:"deployed_apps"`
	DeveloperApps      int    `json:"developer_apps"`
	IncludedGBHours    int64  `json:"included_gb_hours"`
	AppLayerMaxMB      int    `json:"app_layer_max_mb"`
	EphemeralDiskMaxMB int    `json:"ephemeral_disk_max_mb"`
}

// APIKeyResponse is an API key returned to the customer. The plaintext
// appears ONLY on creation (POST /v1/keys), never on GET — only the prefix
// + label + scopes + last_used_at + id are returned thereafter. Scopes is
// the explicit permission set attached to the key (e.g. ["admin"],
// ["apps:read", "deploy:write"]); see ADR-034 rev2.
type APIKeyResponse struct {
	ID         string   `json:"id"`
	Prefix     string   `json:"prefix"` // "fp_live_abc12345…" (first 16 chars)
	Label      string   `json:"label,omitempty"`
	Scopes     []string `json:"scopes"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	CreatedAt  string   `json:"created_at"`
	// Plaintext appears ONLY on the create response, never persisted.
	Plaintext string `json:"plaintext,omitempty"`
}

// CreateKeyRequest is the body of POST /v1/keys. Label is optional
// (max 100 chars per spec); empty label is allowed and renders as
// `{}` so the server's optional-field handling stays in scope. Scopes
// is the requested permission set; the server validates each entry
// against the closed vocabulary (admin, apps:read, deploy:write,
// secrets:read, secrets:write, usage:read) and defaults to
// ["admin"] when omitted so existing callers keep full access. See
// ADR-034 rev2.
type CreateKeyRequest struct {
	Label  string   `json:"label,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

// CustomDomainResponse is a custom domain's wire shape. VerifiedAt is the
// zero time on unverified rows; the verifier goroutine polls DNS and updates
// it (spec §7).
type CustomDomainResponse struct {
	Domain         string `json:"domain"`
	AppID          string `json:"app_id"`
	ChallengeToken string `json:"challenge_token,omitempty"`
	Verified       bool   `json:"verified"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	TXTRecord      string `json:"txt_record,omitempty"` // convenience for the customer
}

// CreateCustomDomainRequest accepts a domain to bind.
type CreateCustomDomainRequest struct {
	Domain string `json:"domain"`
	AppID  string `json:"app_id"`
}

// DomainDoctorReport (ADR-120) is the wire shape for
// GET /v1/domains/{domain}/doctor. Five Render-style check
// lines (dns_record / points_to_gregale / tls_certificate /
// caa_permits / ipv6_conflict) plus the durable row's app_id
// and observed_at. Stale=true means the cached observation
// row was older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default
// 300) when the handler ran a synchronous re-probe.
type DomainDoctorReport struct {
	Domain     string              `json:"domain"`
	AppID      string              `json:"app_id"`
	Stale      bool                `json:"stale,omitempty"`
	ObservedAt string              `json:"observed_at"`
	Healthy    bool                `json:"healthy"`
	Checks     []DomainDoctorCheck `json:"checks"`
}

// DomainDoctorCheck is one row of the doctor report. Stable
// Name tokens (dns_record / points_to_gregale /
// tls_certificate / caa_permits / ipv6_conflict) so the CLI
// can filter by name without parsing the human Detail field.
// Remediation is the exact record to change when Status is
// fail — the load-bearing field for the activation drop-off.
type DomainDoctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Observed    string `json:"observed,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	CheckedAt   string `json:"checked_at,omitempty"`
}

// CronResponse mirrors the crons table. LastFiredAt is the most
// recent fire stamp schedd wrote (MarkCronFired). Zero-valued
// crons serialize as "" — the dashboard only shows the column
// when populated.
type CronResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	Schedule    string `json:"schedule"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	LastFiredAt string `json:"last_fired_at,omitempty"`
}

// CreateCronRequest creates a scheduled synthetic POST.
type CreateCronRequest struct {
	AppID    string `json:"app_id"`
	Schedule string `json:"schedule"`
	Path     string `json:"path,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// UpdateCronRequest is a partial update.
type UpdateCronRequest struct {
	Schedule *string `json:"schedule,omitempty"`
	Path     *string `json:"path,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

// InstanceResponse is the read-only instance view (spec §4.2 / §6).
type InstanceResponse struct {
	ID            string `json:"id"`
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
	State         string `json:"state"`
	HostIP        string `json:"host_ip,omitempty"`
	RAMMB         int    `json:"ram_mb"`
	StartedAt     string `json:"started_at,omitempty"`
	LastRequestAt string `json:"last_request_at,omitempty"`
	ParkedAt      string `json:"parked_at,omitempty"`
	// WakeID is the per-wake stable identifier minted by schedd at
	// CreateInstance time (UUIDv7). Distinct from `id` (the row PK):
	// one row can carry many WakeIDs over its lifetime as the app is
	// parked and re-woken. Surfaced on `faas ps` and the dashboard
	// detail page so operators can correlate the request that woke
	// the app against gateway logs and slog entries (which also
	// carry this field).
	WakeID string `json:"wake_id,omitempty"`
}

// UsageResponse is one app's monthly usage slice (spec §10).
type UsageResponse struct {
	AppID     string `json:"app_id"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
	// IncludedGBHours is the included quota for the account's plan at the
	// requested month; the CLI computes the overage from this and the rows.
	IncludedGBHours int64 `json:"included_gb_hours"`
}

// DeploymentListResponse is the page shape for GET /v1/deployments.
// Items is the page (in created_at DESC order); NextBefore is the
// cursor the caller should pass on the next request to page BACKWARDS
// (the dashboard's "older deploys" link). Empty NextBefore means the
// page is the end of the list.
//
// Cursor format: RFC3339Nano (matches state.Deployment.CreatedAt).
type DeploymentListResponse struct {
	Items      []DeploymentResponse `json:"items"`
	NextBefore string               `json:"next_before,omitempty"`
}

// --- Dashboard auth (issue #165, ADR-032 PR #2) ----------------------------

// OAuthProvider is the issuer name used by the dashboard OAuth flows
// (the email/identity brokers). The set is intentionally closed — adding
// a new provider is a Store + handler + OpenAPI change, not a config
// flag. "google" and "github" are wired in PR #2.
type OAuthProvider string

const (
	OAuthProviderGoogle OAuthProvider = "google"
	OAuthProviderGitHub OAuthProvider = "github"
)

// PasswordLoginRequest is the body of POST /login. The email is the
// canonical handle (lowercase + trim — the handler runs the same
// canonicalisation the account-create path uses so an "alice@example.com
// vs ALICE@example.com" login pair collapses to one row). Password is
// the plaintext the client sent over TLS; the Argon2id verify is in
// pkg/auth.Verify and runs on the server only.
type PasswordLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// PasswordLoginResponse is what POST /login (and POST /signup) return
// on success. The session cookie rides on the Set-Cookie header — the
// body deliberately carries NO api_key field. Pre-#165 (PR #1) the
// response minted a "web-console" key and returned it in the body; that
// was the takeover surface. The SDK path is the device-code CLI
// (MintCliAuthCode / ExchangeCliAuthCode), not a login-bundled key, so
// removing the field here doesn't break programmatic auth.
type PasswordLoginResponse struct {
	AccountID string `json:"account_id"`
	Plan      string `json:"plan"`
}

// PasswordSignupRequest is the body of POST /signup. Same shape as
// PasswordLoginRequest — we accept the same argon2id-shaped ciphertext
// at signup and re-verify at login, so the handler-side error
// equivalence ("wrong password" vs "no account" vs "weak password") is
// kept intact under the same JSON keys.
type PasswordSignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ProgrammaticAuthResponse is the body for the JSON-only
// POST /v1/auth/{signup,login} pair (issue #311). Distinct from
// PasswordLoginResponse: this one carries the api_key payload so the
// bearer-key CLI can use the result without a dashboard round-trip.
//
// Email is echoed back so SDK callers can render "Logged in as
// <email>" without an extra Whoami round-trip. Mirrors pkg/api's
// ProgrammaticAuthResponse — drift fix tracked in PR #793
// (pre-existing from PR #786).
type ProgrammaticAuthResponse struct {
	AccountID string             `json:"account_id"`
	Email     string             `json:"email"`
	Plan      string             `json:"plan"`
	APIKey    ProgrammaticAPIKey `json:"api_key"`
}

// ProgrammaticAPIKey is the freshly minted API key returned on the
// first request. Plaintext is returned ONCE; the caller persists it.
type ProgrammaticAPIKey struct {
	Plaintext string `json:"plaintext"`
	Prefix    string `json:"prefix"`
	ID        string `json:"id"`
}

// MagicLinkSignupRequest is the body of POST /v1/auth/signup/magic-link.
// The email is optional — the handler accepts a missing or unparseable
// email and still returns 200 so the response cannot be used to
// enumerate accounts.
type MagicLinkSignupRequest struct {
	Email string `json:"email"`
}

// PasswordResetRequest is the body of POST /login/forgot. The email
// is optional — the same-shape internal handler is hit by the form
// page (no body) and the SDK (email in body). The handler always
// returns 200 with an identical body and identical timing whether or
// not the email exists, so the surface does not leak account presence.
type PasswordResetRequest struct {
	Email string `json:"email,omitempty"`
}

// PasswordResetConfirm is the body of POST /auth/reset. Token is the
// 32-byte value the email link carried (base64url-encoded, NOT the
// SHA-256 hash the server stored). NewPassword is the plaintext the
// user is opting into; the server Argon2id-encodes it server-side and
// runs ConsumeLoginToken atomically so a replay returns 410.
type PasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// SetPasswordRequest is the body of POST /dashboard/account/set-password.
// Lets OAuth-only users opt into password login. Same shape as the
// reset-confirm NewPassword field — the handler runs auth
// (sessionAuth) before encoding, so this is an authenticated surface.
type SetPasswordRequest struct {
	Password  string `json:"password"`
	CSRFToken string `json:"csrf_token"`
	// CurrentPassword is required when the account already has a
	// password and the session carries no fresh step-up (ADR-140).
	// Ignored for OAuth-only accounts, which have nothing to verify.
	CurrentPassword string `json:"current_password,omitempty"`
}

// UsageSummaryResponse is the roll-up for the current month (or any
// month passed as a query param). Used by the dashboard usage page so
// the customer sees a single number ("used X of Y GB-h, overage $Z")
// without having to sum rows.
//
// Overage math: anything above IncludedGBHours is billable at the
// overage rate in the financial model (€0.01/GB-h). Cents are integer.
type UsageSummaryResponse struct {
	Month           string  `json:"month"`             // YYYY-MM
	UsedGBHours     float64 `json:"used_gb_hours"`     // Σ mb_seconds / 3_600_000
	IncludedGBHours int64   `json:"included_gb_hours"` // from plan limits
	OverageGBHours  float64 `json:"overage_gb_hours"`  // max(0, used - included)
	OverageCents    int64   `json:"overage_cents"`     // overage * 1.0 (€0.01/GB-h in cents)
}

// ValidateAppConfig checks a requested app config against its plan caps (spec
// §4.2: validation before work). It returns the first violating *Problem, or nil.
// The deployed-app COUNT check is done in apid (it needs the store).
func ValidateAppConfig(l Limits, ramMB, maxConcurrency int) *Problem {
	if ramMB > l.RAMMB {
		return ErrPlanLimitRAM(l, ramMB)
	}
	if maxConcurrency > l.MaxConcurrency {
		return NewProblem(403, CodePlanLimitConcur,
			"Concurrency over plan limit",
			fmt.Sprintf("%s plan caps max_concurrency at %d; requested %d.", l.Plan, l.MaxConcurrency, maxConcurrency)).
			WithLimit(int64(l.MaxConcurrency), int64(maxConcurrency)).
			WithDocs(docsBase + "/plans#concurrency")
	}
	return nil
}

// --- G6 account self-service (spec §17 G6, ADR-021) -------------------------

// AccountExportResponse is the GET /v1/account/export bundle. A
// single JSON document with one slice per resource type the customer
// owns (apps, deployments, builds, instances, usage, domains, crons,
// API keys, app_secrets). Ciphertext passthrough for the secrets
// slice — the plaintext VALUE never lands in PG (ADR-020), so the
// customer can rotate their host age key after a restore-from-export
// without losing the per-secret envelope.
type AccountExportResponse struct {
	ExportedAt  string                    `json:"exported_at"`
	Account     AccountResponse           `json:"account"`
	Apps        []AppResponse             `json:"apps"`
	Deployments []DeploymentResponse      `json:"deployments"`
	Builds      []BuildExportResponse     `json:"builds"`
	Instances   []InstanceResponse        `json:"instances"`
	Usage       []UsageExportResponse     `json:"usage"`
	Domains     []CustomDomainResponse    `json:"domains"`
	Crons       []CronResponse            `json:"crons"`
	APIKeys     []APIKeyExportResponse    `json:"api_keys"`
	AppSecrets  []AppSecretExportResponse `json:"app_secrets"`
	// AuditTrail is the customer's own GDPR ledger slice: every
	// export/delete/restore the customer has hit. Surfaced in the
	// bundle so the export is self-describing (the customer can see
	// "yes, my last deletion request fired at <ts>") without a
	// separate GET round trip.
	AuditTrail []GdprAuditExportResponse `json:"audit_trail,omitempty"`
}

// BuildExportResponse is the per-build row in the export bundle.
// Reduced shape (no internal IDs the customer can't act on).
type BuildExportResponse struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	AppID        string `json:"app_id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	SourceBytes  int64  `json:"source_bytes"`
	StartedAt    string `json:"started_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// UsageExportResponse is the per-month roll-up in the export bundle.
// `month` is YYYY-MM (matches the dashboard's usage page render).
type UsageExportResponse struct {
	AppID     string `json:"app_id"`
	Month     string `json:"month"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
}

// APIKeyExportResponse is one row in the export's API key slice.
// The plaintext key never appears here (and never reappears after
// the create response, per §4.2). Only the prefix + label + scopes +
// timestamps. Scopes is included so the customer's GDPR export carries
// the full audit trail of which keys had which permissions at the
// moment of export (ADR-034 rev2).
type APIKeyExportResponse struct {
	ID        string   `json:"id"`
	Prefix    string   `json:"prefix"`
	Label     string   `json:"label,omitempty"`
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
	LastUsed  string   `json:"last_used_at,omitempty"`
}

// GdprAuditExportResponse is one row of the customer's own audit trail
// as surfaced in the export bundle. Two row kinds live here:
//
//   - source="gdpr"   — a self-service GDPR action (export/delete/restore
//     from the gdpr_requests table). Action is "export" | "delete" |
//     "restore"; CompletedAt is empty when the action is still in
//     flight.
//   - source="event"  — a security event from the events table (IAM-4,
//     ADR-035). Kind is the namespaced event kind (e.g. "auth.login",
//     "key.created"); Data is the original jsonb payload.
//
// Rows from both sources are interleaved by timestamp descending in
// the bundle so a reviewer sees one ordered timeline. Existing GDPR
// consumers can ignore unknown fields per the standard JSON rule.
type GdprAuditExportResponse struct {
	Source      string          `json:"source"`           // "gdpr" | "event"
	Action      string          `json:"action,omitempty"` // "export" | "delete" | "restore" (gdpr)
	RequestedAt string          `json:"requested_at"`     // RFC 3339 (event.at for source="event")
	CompletedAt string          `json:"completed_at,omitempty"`
	Kind        string          `json:"kind,omitempty"` // auth.*|key.*|account.*|secret.* (event)
	Data        json.RawMessage `json:"data,omitempty"` // kind-specific payload (event)
}

// AppSecretExportResponse is one row in the export's app_secrets slice.
// Ciphertext is the age-sealed envelope (base64). Plaintext never
// lands here — the customer imports the envelope into another faas
// install (or their own age tool) to unseal.
type AppSecretExportResponse struct {
	AppID      string `json:"app_id"`
	Key        string `json:"key"`
	Ciphertext string `json:"ciphertext"` // base64
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// AccountDeletionResponse is the response from DELETE /v1/account
// (and the same shape is replayed on every repeat call — the
// idempotent endpoint guarantees the response body is identical
// across retries inside the 24 h window).
type AccountDeletionResponse struct {
	Status       string `json:"status"`        // always "deleted_pending"
	ScheduledAt  string `json:"scheduled_at"`  // deletion_requested_at, RFC 3339
	RestoreUntil string `json:"restore_until"` // scheduled_at + 30 d, RFC 3339
}

// StatusPage is the JSON shape served by GET /status/slo.json (spec
// §12, M8 acceptance). Lives in pkg/api so the CLI can import it
// without a back-reference into cmd/apid; cmd/apid/status.go embeds
// the same JSON tags so the wire shape stays identical.
//
// Fields are documented in deploy/statuspage/index.html; renames here
// must propagate to that file (and to the statusCache JSON encoder in
// cmd/apid/status.go).
type StatusPage struct {
	// APIAvailabilityPct is the rolling 5-minute 2xx rate over
	// gateway_requests_total, expressed 0..100.
	APIAvailabilityPct float64 `json:"api_availability_pct"`
	// WakeP95MS is the p95 of gateway_wake_latency_seconds over the
	// last 5 minutes, in milliseconds.
	WakeP95MS float64 `json:"wake_p95_ms"`
	// BuildSuccessPct is the rolling 5-minute success rate of
	// builderd builds (completed/success ÷ (completed/success +
	// completed/failure)).
	BuildSuccessPct float64 `json:"build_success_pct"`
	// Degraded is true when at least one page- or warn-severity alert
	// is currently firing on the local Prometheus. The public status
	// page renders a "degraded" pill when this is true so prospects
	// and customers see the same picture the operator's pager sees.
	//
	// The flag is intentionally conservative: a transient PromQL
	// error against ALERTS{} is treated as "no firing alerts" rather
	// than poisoning the snapshot. Prometheus being completely
	// unreachable still surfaces via Source = "degraded: <reason>"
	// (the pre-existing contract).
	Degraded bool `json:"degraded"`
	// AsOf is the UTC timestamp the snapshot was taken. The HTML
	// renders "Updated 3 min ago" off this.
	AsOf time.Time `json:"as_of"`
	// Source is "prometheus", "degraded: firing alerts", or
	// "degraded: <reason>" so an operator tailing the JSON can tell
	// at a glance why a snapshot is or isn't trustworthy.
	Source string `json:"source"`
}

// --- Move 2: event-driven surface response shapes ----------------------------
//
// AsyncInvokeResponse is the 202-side of POST /v1/apps/{slug}/invoke/async.
// StatusURL is the well-known read endpoint so the dashboard (and the
// SDK) can poll without parsing the id.
type AsyncInvokeResponse struct {
	ID        string `json:"id"`
	StatusURL string `json:"status_url"`
}

// InvokeResponse is the sync-side of POST /v1/apps/{slug}/invoke.
// Status is the final row state (one of "completed" | "failed"
// | "cancelled"); Result is the per-app payload the drain stamped
// (nil while pending, populated by drain.emitDone).
type InvokeResponse struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

// QueueSendResponse is returned on POST /v1/apps/{slug}/queues/invocations:send.
// 201 Created with the new id; the customer pairs this with the
// /receive long-poll.
type QueueSendResponse struct {
	ID string `json:"id"`
}

// QueueReceiveResponse is returned on POST /v1/apps/{slug}/queues/invocations:receive.
// 200 with the dequeued row's payload + result; 204 on timeout.
type QueueReceiveResponse struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// DelayedTaskResponse is the create/get shape for delayed tasks.
// ScheduledAt is the customer-facing UTC dispatch time; State is
// populated on get, omitted on create (always "pending" there).
type DelayedTaskResponse struct {
	ID          string    `json:"id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	State       string    `json:"state,omitempty"`
}

// ListInvocationsResponse lives in cmd/apid because pkg/api cannot
// import pkg/state (cyclic). The handler-local type is `[]state.Invocation`
// — the wire shape is identical, only the package differs.

// InvokeRequest is the body for POST /v1/apps/{slug}/invoke[/async].
// Method defaults to POST; path defaults to `/` (the handler fills
// defaults; the zero values are not persisted).
type InvokeRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
	Method  string          `json:"method,omitempty"`
	Path    string          `json:"path,omitempty"`
}

// QueueSendRequest is the body for POST /v1/apps/{slug}/queues/send.
// Cap-checked against MaxQueueDepth at the handler.
type QueueSendRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
}

// DelayedTaskRequest is the body for POST /v1/apps/{slug}/delayed-tasks.
// ScheduledAt must be in the future (UTC); the handler rejects past
// timestamps with invalid_scheduled_at.
type DelayedTaskRequest struct {
	Payload     json.RawMessage `json:"payload,omitempty"`
	ScheduledAt time.Time       `json:"scheduled_at"`
}

// Invocation is the SDK-side mirror of state.Invocation. The wire
// is the same JSON the handler emits (writeJSON(w, 200, inv) where
// inv is a state.Invocation), but pkg/api cannot import pkg/state
// (import cycle — state pkg is the lowest layer). The mirror is
// exhaustive: every field with a JSON tag on state.Invocation gets a
// typed row here so the SDK gets proper Go types and JSON tags. The
// name `Invocation` matches the OpenAPI schema (api/openapi.yaml
// `Invocation`) so the spec_compliance test sees a 1:1 mapping.
type Invocation struct {
	ID             string          `json:"id"`
	AppID          string          `json:"app_id"`
	AccountID      string          `json:"account_id"`
	InstanceID     string          `json:"instance_id,omitempty"`
	Source         string          `json:"source"`
	State          string          `json:"state"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Payload        json.RawMessage `json:"payload"`
	Headers        json.RawMessage `json:"headers"`
	DueAt          time.Time       `json:"due_at"`
	ScheduledAt    *time.Time      `json:"scheduled_at,omitempty"`
	AckURL         string          `json:"ack_url,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	ReceivedAt     *time.Time      `json:"received_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	Attempts       int             `json:"attempts"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ListInvocationsResponse is the wire shape for GET /v1/invocations.
// The handler emits a `[]state.Invocation` under the `invocations`
// key; here we declare the same shape with the SDK-side mirror type
// so pkg/api stays decoupled from pkg/state.
type ListInvocationsResponse struct {
	Invocations []Invocation `json:"invocations"`
}

// --- IAM-4 (ADR-035) — auth audit event surface -----------------------------
//
// AuditEventResponse is one row of the customer's own security event
// timeline. The kind taxonomy is documented in
// docs/adr/035-auth-audit-events.md; common values include
// "auth.login", "auth.logout", "key.created", "key.deleted",
// "secret.set", "secret.deleted", "account.plan_changed",
// "account.deletion_scheduled", "account.deletion_restored".
//
// Subject is the account_id the event was recorded against (string,
// not the raw uuid UUID type — pkg/api stays string-typed for wire
// stability). Data is the raw jsonb the apid auditor wrote; the schema
// varies by kind and is documented per-kind in the ADR.
//
// Severity (Mega-PR B) is the highest-severity classification for
// stateless.advisory rows; "" for all other kinds. omitempty keeps
// the wire shape stable for non-stateless kinds and pre-PR-427 rows.
// Mirrors pkg/api.AuditEventResponse — keep in lockstep with the
// canonical DTO via the spec-sync invariant
// (`make sdk-check`).
type AuditEventResponse struct {
	ID       string          `json:"id"`    // bigint as string
	At       string          `json:"at"`    // RFC 3339
	Actor    string          `json:"actor"` // "apid" today; "schedd" for state-transition events
	Kind     string          `json:"kind"`
	Subject  string          `json:"subject,omitempty"` // account_id (uuid string form)
	Severity string          `json:"severity,omitempty"`
	Data     json.RawMessage `json:"data"`
}

// ListAuditEventsResponse is the wire shape for GET /v1/audit-events.
// Limit echoes the effective limit applied by the handler (capped at
// 100), so the SDK can display "showing 50 of N" without re-issuing
// the request.
type ListAuditEventsResponse struct {
	Events []AuditEventResponse `json:"events"`
	Limit  int                  `json:"limit"`
}

// WakeTimelineEvent is one frame in the canonical wake timeline
// (issue #517 / PR-C / ADR-064). The SDK treats Data as a generic
// map (the canonical vocabulary — queue_accepted, admitted,
// boot_started, boot_completed, boot_failed, readiness_200,
// proxy_first_byte, park_started, park_completed, stalled,
// build_succeeded, build_failed, deploy_failed — is documented in
// ADR-064; a typed accessor that downcasts the Data map is
// straightforward but not provided here to keep the SDK surface
// schematic).
//
// At is RFC 3339 with nanosecond precision so frames that land in
// the same wall-clock second still preserve at-ordering in
// lexicographic string compare (the handler orders at ASC, oldest
// first).
type WakeTimelineEvent struct {
	At    string         `json:"at"`
	Kind  string         `json:"kind"`
	Actor string         `json:"actor"`
	Data  map[string]any `json:"data"`
}

// WakeTimelineResponse is the wire shape for
// GET /v1/apps/{slug}/wakes/{wake_id}/timeline. NextCursor is the
// last row's `at` formatted as RFC 3339 Nano — the caller passes
// it back as ?since= to read the next page.
type WakeTimelineResponse struct {
	WakeID     string              `json:"wake_id"`
	AppID      string              `json:"app_id"`
	Events     []WakeTimelineEvent `json:"events"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Limit      int                 `json:"limit"`
}

// AppMetricsResponse is the per-app metrics payload returned by
// GET /v1/apps/{slug}/metrics?range= (issue #273 / ADR-042). Field-
// for-field mirror of pkg/api.AppMetricsResponse — the SDK parity
// gate (cmd/sdk-coverage) enforces byte-identical JSON output.
type AppMetricsResponse struct {
	AppID        string  `json:"app_id"`
	Range        string  `json:"range"`
	Source       string  `json:"source"`
	AsOf         string  `json:"as_of"`
	RequestCount int64   `json:"request_count"`
	LatencyP50MS float64 `json:"latency_p50_ms"`
	LatencyP95MS float64 `json:"latency_p95_ms"`
	LatencyP99MS float64 `json:"latency_p99_ms"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	ColdStartPct float64 `json:"cold_start_pct"`
	WakeP95MS    float64 `json:"wake_p95_ms"`
}

// SLODuration is the shared latency sub-shape used by
// AppSLOResponse and AccountSLOResponse. Field-for-field
// mirror of pkg/api.SLODuration — the SDK parity gate
// (cmd/sdk-coverage) enforces byte-identical JSON output.
type SLODuration struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

// AppSLOResponse is the per-app SLO panel returned by
// GET /v1/apps/{slug}/slo?window= (issue #696 / ADR-082).
// Field-for-field mirror of pkg/api.AppSLOResponse.
type AppSLOResponse struct {
	AppID           string      `json:"app_id"`
	AppSlug         string      `json:"app_slug"`
	Window          string      `json:"window"`
	Source          string      `json:"source"`
	AsOf            string      `json:"as_of"`
	RequestDuration SLODuration `json:"request_duration"`
	ErrorRatePct    float64     `json:"error_rate_pct"`
	ColdBootRatePct float64     `json:"cold_boot_rate_pct"`
	InstanceHours   float64     `json:"instance_hours"`
	GBHours         float64     `json:"gb_hours"`
	WakeQueueP95MS  float64     `json:"wake_queue_p95_ms"`
	RequestsTotal   int64       `json:"requests_total"`
	ThrottledTotal  int64       `json:"throttled_total"`
}

// AccountSLOResponse is the account-wide SLO rollup returned
// by GET /v1/account/slo?window= (issue #696 / ADR-082).
// Field-for-field mirror of pkg/api.AccountSLOResponse.
type AccountSLOResponse struct {
	Window          string      `json:"window"`
	Source          string      `json:"source"`
	AsOf            string      `json:"as_of"`
	RequestDuration SLODuration `json:"request_duration"`
	ErrorRatePct    float64     `json:"error_rate_pct"`
	ColdBootRatePct float64     `json:"cold_boot_rate_pct"`
	InstanceHours   float64     `json:"instance_hours"`
	GBHours         float64     `json:"gb_hours"`
	WakeQueueP95MS  float64     `json:"wake_queue_p95_ms"`
	RequestsTotal   int64       `json:"requests_total"`
	ThrottledTotal  int64       `json:"throttled_total"`
}

// Org surface (issue #190 / IAM-6 / ADR-061, PR 5). The wire
// shapes mirror pkg/api/orgs.go (the canonical source) byte-for-byte
// so the public SDK surface stays consistent with pkg/api.Client —
// the sdk-coverage gate (cmd/sdk-coverage) reads pkg/api/*.go and
// fails on any spec-route/method drift, which is the contract
// we're upholding here. The redeclaration (rather than type-aliasing)
// is forced by the separate-module layout: sdk/go is its own Go
// module (module github.com/poyrazK/faas/sdk/go) and cannot import
// pkg/api (which lives in module github.com/onebox-faas/faas).

// CreateOrgRequest is the body of POST /v1/orgs.
type CreateOrgRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// PatchOrgRequest is the body of PATCH /v1/orgs/{slug}. Both fields
// are pointer-typed so the wire form distinguishes "not set" from
// "clear".
type PatchOrgRequest struct {
	Name *string `json:"name,omitempty"`
	Plan *string `json:"plan,omitempty"`
}

// InviteMemberRequest is the body of POST /v1/orgs/{slug}/members.
// Role cannot be `owner`; transfer-ownership is the only path.
type InviteMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// ChangeMemberRoleRequest is the body of PATCH
// /v1/orgs/{slug}/members/{user_id}. Role cannot be `owner`.
type ChangeMemberRoleRequest struct {
	Role string `json:"role"`
}

// TransferOwnershipRequest is the body of POST
// /v1/orgs/{slug}/transfer_ownership.
type TransferOwnershipRequest struct {
	NewOwnerAccountID string `json:"new_owner_account_id"`
}

// OrgResponse is the canonical org wire shape.
type OrgResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Personal  bool   `json:"personal"`
	Plan      string `json:"plan"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// OrgListResponse is the body of GET /v1/orgs. Sorted by slug.
type OrgListResponse struct {
	Orgs []OrgResponse `json:"orgs"`
}

// OrgMemberResponse is the wire shape for a single org membership row.
type OrgMemberResponse struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	JoinedAt  string `json:"joined_at"`
}

// MemberListResponse is the body of GET /v1/orgs/{slug}/members.
type MemberListResponse struct {
	Members []OrgMemberResponse `json:"members"`
}

// OrgInvitationResponse is the wire shape for a single invitation row.
// Plaintext token is NEVER re-served — returned ONCE on the create
// call via InvitationWithTokenResponse.
type OrgInvitationResponse struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	OrgSlug   string `json:"org_slug"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

// InvitationWithTokenResponse is the body of POST
// /v1/orgs/{slug}/members — the canonical invitation shape plus the
// one-time plaintext token.
type InvitationWithTokenResponse struct {
	OrgInvitationResponse
	Token string `json:"token"`
}

// InvitationListResponse is the body of GET
// /v1/orgs/{slug}/invitations. Sorted by created_at DESC.
type InvitationListResponse struct {
	Invitations []OrgInvitationResponse `json:"invitations"`
}

// SeatUsageResponse is the body of GET
// /v1/orgs/{slug}/seat_usage. Visibility-only — PR 9 ships the
// per-seat pricing cut-over per ADR-061 §"Out of scope". `limit`
// returns 0 for the free plan (the fail-closed accessor shape
// the dashboard renders as "personal org only").
type SeatUsageResponse struct {
	Used  int    `json:"used"`
	Limit int    `json:"limit"`
	Plan  string `json:"plan"`
}

// ChangeMemberRoleRequest is the body of PATCH
// /v1/orgs/{slug}/members/{user_id}. Role cannot be `owner`.

// ScanSeverityCounts is the per-severity CVE tally on a per-deploy
// grype scan (issue #464 / ADR-075). Each counter is the number of
// findings at that severity level; a deploy with no findings has all
// five at zero.
type ScanSeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

// ScanVulnerability is a single CVE finding on a per-deploy grype
// scan. The fields mirror the grype JSON shape the apid handler
// (`cmd/apid/handlers_scan.go`) forwards verbatim — see
// pkg/imaged.Vulnerability for the upstream definition.
//
// Extension (issue #464 / PR-B acceptance): Paths carries the
// per-file path list from grype's artifact.locations[].path. The
// PR-651 review finding #54 ("customers don't need internal
// grype match paths") was revisited when the dashboard's "Path"
// column was added; the per-file paths help customers identify
// which shared library to rebuild or replace. pkg/api's
// marshalling is `paths,omitempty`, so a no-path CVE stays
// compact on the wire.
type ScanVulnerability struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"` // CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN
	Package  string   `json:"package"`
	Version  string   `json:"version"`
	FixedIn  string   `json:"fixed_in,omitempty"`
	Paths    []string `json:"paths,omitempty"`
}

// ScanResult is the wire shape returned by GET
// /v1/deployments/{id}/scan (issue #464 / ADR-075 — per-deploy
// grype CVE scan surface). The Status field is the closed enum
// (complete|failed|skipped); see pkg/api.ScanResult for the full
// shape and `pkg/api/limits.go` for the closed-enum definition.
// Err is populated only when Status="failed"; grype exit message
// is the value.
type ScanResult struct {
	Status          string              `json:"status"`
	ScannedAt       string              `json:"scanned_at,omitempty"`
	ScannerVersion  string              `json:"scanner_version,omitempty"`
	ImageDigest     string              `json:"image_digest,omitempty"`
	SeverityCounts  ScanSeverityCounts  `json:"severity_counts"`
	Vulnerabilities []ScanVulnerability `json:"vulnerabilities"`
	Err             string              `json:"error,omitempty"`
}

// --- Webhook delivery (issue #476 / ADR-076) -----------------------------
// --- Webhook delivery (issue #476 / ADR-076) -----------------------------
//
// Wire shape mirrors pkg/api/webhooks.go. Field names follow the
// openapi.yaml kebab-case convention (target_url, event_filter, …)
// so the embedded copy in pkg/apid/openapi.yaml stays in sync with
// `make spec-sync`. All secret fields are surfaced as
// `WebhookSecretSealedMasked: "***"` constants — the plaintext
// never appears in any response (NOT even on rotate-secret — see
// ADR-076 §3.7).

// CreateAppWebhookRequest is the body of POST
// /v1/apps/{slug}/webhooks. The plaintext WebhookSecret is sent
// over the wire and is NEVER echoed in any other response.
type CreateAppWebhookRequest struct {
	TargetURL     string   `json:"target_url"`
	WebhookSecret string   `json:"webhook_secret"`
	EventFilter   []string `json:"event_filter,omitempty"`
	RetryPolicy   string   `json:"retry_policy,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

// UpdateAppWebhookRequest is the body of PATCH
// /v1/apps/{slug}/webhooks/{id}. Pointer fields let the caller
// distinguish "leave as-is" from "set to empty". Setting
// WebhookSecret to a non-nil string inlines a secret rotation
// (the existing secret is overwritten in place; no plaintext
// is returned).
type UpdateAppWebhookRequest struct {
	TargetURL     *string   `json:"target_url,omitempty"`
	WebhookSecret *string   `json:"webhook_secret,omitempty"`
	EventFilter   *[]string `json:"event_filter,omitempty"`
	RetryPolicy   *string   `json:"retry_policy,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
}

// AppWebhookResponse is the read shape for a single subscription.
// WebhookSecretSealedMasked is always the literal "***"; the
// plaintext never appears here.
type AppWebhookResponse struct {
	ID                        string    `json:"id"`
	AppID                     string    `json:"app_id"`
	AccountID                 string    `json:"account_id"`
	TargetURL                 string    `json:"target_url"`
	WebhookSecretSealedMasked string    `json:"webhook_secret_sealed_masked"`
	EventFilter               []string  `json:"event_filter"`
	RetryPolicy               string    `json:"retry_policy"`
	Enabled                   bool      `json:"enabled"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

// RotateAppWebhookSecretResponse is the body of POST
// /v1/apps/{slug}/webhooks/{id}/rotate-secret. The server mints
// the new plaintext internally and persists it sealed; the wire
// carries only the masked constant and the rotated_at timestamp.
// Per ADR-076 §3.7, the plaintext is NEVER returned over the wire
// — the caller has no way to retrieve it, so it must be fetched
// out-of-band from the original provisioning flow.
type RotateAppWebhookSecretResponse struct {
	WebhookSecretSealedMasked string    `json:"webhook_secret_sealed_masked"`
	RotatedAt                 time.Time `json:"rotated_at"`
}

// AppWebhookDeliveryResponse is one row of the delivery ledger.
type AppWebhookDeliveryResponse struct {
	ID               string     `json:"id"`
	WebhookID        string     `json:"webhook_id"`
	AppID            string     `json:"app_id"`
	AccountID        string     `json:"account_id"`
	Event            string     `json:"event"`
	Attempt          int        `json:"attempt"`
	Status           string     `json:"status"`
	LastError        string     `json:"last_error,omitempty"`
	LastResponseCode int        `json:"last_response_code"`
	NextAttemptAt    time.Time  `json:"next_attempt_at"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AppWebhookDeliveryListResponse is the body of GET
// /v1/apps/{slug}/webhooks/{id}/deliveries. NextToken is empty
// when the listing is exhausted.
type AppWebhookDeliveryListResponse struct {
	Deliveries []AppWebhookDeliveryResponse `json:"deliveries"`
	NextToken  string                       `json:"next_token,omitempty"`
}

// AppWebhookRetryDeliveryResponse is the body of POST
// /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry.
type AppWebhookRetryDeliveryResponse struct {
	Delivery AppWebhookDeliveryResponse `json:"delivery"`
}

// ListAppWebhookDeliveriesOptions are the query knobs for
// Client.ListAppWebhookDeliveries.
type ListAppWebhookDeliveriesOptions struct {
	Status    string
	PageSize  int
	PageToken string
}

// SetAccountEgressAllowlistExtraRequest is the body of PATCH
// /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082).
// Extra is the per-account additive budget on top of the plan's
// apps.egress_allowlist cap. 0 = no override; the plan cap is
// authoritative.
type SetAccountEgressAllowlistExtraRequest struct {
	Extra int `json:"extra"`
}

// AccountEgressAllowlistExtraResponse is the body of GET/PATCH
// /v1/account/egress_allowlist_extra. The PlanCap and MaxExtra
// fields are unconditionally populated so the dashboard can render
// the "Override: N / Plan cap: 16 / Max extra: 1024" trio without
// a second round-trip.
type AccountEgressAllowlistExtraResponse struct {
	Extra    int `json:"extra"`
	PlanCap  int `json:"plan_cap"`
	MaxExtra int `json:"max_extra"`
}

// --- Triggers (issue #757 / ADR-100) ----------------------------------------
// Wire shape for the unified event-source-mapping primitive. The
// discriminator is TriggerKind. Five non-cron kinds share the same
// batch/filter/retry/ReportBatchItemFailures machinery; cron keeps
// (schedule, path) for backward compat with the robfig schedule
// parser.

// TriggerKind is the closed-vocabulary discriminator.
type TriggerKind string

const (
	TriggerKindCron         TriggerKind = "cron"
	TriggerKindKafka        TriggerKind = "kafka"
	TriggerKindNATS         TriggerKind = "nats"
	TriggerKindRedisStreams TriggerKind = "redis_streams"
	TriggerKindSQSCompat    TriggerKind = "sqs_compat"
	TriggerKindQueue        TriggerKind = "queue"
)

// Trigger is the wire shape returned by GET/POST/PATCH /v1/triggers.
type Trigger struct {
	ID            string          `json:"id"`
	AccountID     string          `json:"account_id"`
	AppID         string          `json:"app_id"`
	Kind          TriggerKind     `json:"kind"`
	Slug          string          `json:"slug,omitempty"`
	Enabled       bool            `json:"enabled"`
	Config        json.RawMessage `json:"config"`
	BatchSizeMax  int             `json:"batch_size_max"`
	BatchWindowMs int             `json:"batch_window_ms"`
	MaxAttempts   int             `json:"max_attempts"`
	Schedule      string          `json:"schedule,omitempty"`
	Path          string          `json:"path,omitempty"`
	CronID        string          `json:"cron_id,omitempty"`
	Source        *string         `json:"source,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// CreateTriggerRequest creates a new trigger. Kind is immutable.
type CreateTriggerRequest struct {
	AppID         string          `json:"app_id"`
	Kind          TriggerKind     `json:"kind"`
	Slug          string          `json:"slug,omitempty"`
	Enabled       *bool           `json:"enabled,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	BatchSizeMax  *int            `json:"batch_size_max,omitempty"`
	BatchWindowMs *int            `json:"batch_window_ms,omitempty"`
	MaxAttempts   *int            `json:"max_attempts,omitempty"`
	Schedule      string          `json:"schedule,omitempty"`
	Path          string          `json:"path,omitempty"`
}

// UpdateTriggerRequest is a partial update.
type UpdateTriggerRequest struct {
	Enabled       *bool           `json:"enabled,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	BatchSizeMax  *int            `json:"batch_size_max,omitempty"`
	BatchWindowMs *int            `json:"batch_window_ms,omitempty"`
	MaxAttempts   *int            `json:"max_attempts,omitempty"`
	Schedule      *string         `json:"schedule,omitempty"`
	Path          *string         `json:"path,omitempty"`
}

// TriggerRecord is the per-record audit row surfaced via GET
// /v1/triggers/{id}/records.
type TriggerRecord struct {
	ID               string     `json:"id"`
	TriggerID        string     `json:"trigger_id"`
	ItemIdentifier   string     `json:"item_identifier"`
	Payload          string     `json:"payload"`
	Headers          string     `json:"headers"`
	Metadata         string     `json:"metadata"`
	State            string     `json:"state"`
	Attempts         int        `json:"attempts"`
	NextFireAt       time.Time  `json:"next_fire_at"`
	ReceivedAt       time.Time  `json:"received_at"`
	LastError        *string    `json:"last_error,omitempty"`
	LastDispatchedAt *time.Time `json:"last_dispatched_at,omitempty"`
}

// TriggerRecordRetryRequest is the body of POST
// /v1/triggers/{id}/records/{rid}/retry.
type TriggerRecordRetryRequest struct{}

// TriggerDeadLetter is the wire shape for one trigger_dead_letter row.
type TriggerDeadLetter struct {
	RecordID  string    `json:"record_id"`
	TriggerID string    `json:"trigger_id"`
	Reason    string    `json:"reason"`
	RoutedTo  string    `json:"routed_to"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTriggerRecordsResponse answers GET /v1/triggers/{id}/records.
type ListTriggerRecordsResponse struct {
	Records []TriggerRecord `json:"records"`
}

// ListTriggerDeadLetterResponse answers GET /v1/triggers/{id}/dlq.
type ListTriggerDeadLetterResponse struct {
	Records []TriggerDeadLetter `json:"records"`
}

// CreateTriggerBatchRequest is the body of POST
// /v1/triggers:batch_create.
type CreateTriggerBatchRequest struct {
	AppID        string `json:"app_id"`
	ManifestYAML string `json:"manifest_yaml"`
}

// TriggerMetricsResponse is the body of GET /v1/triggers/{id}/metrics.
type TriggerMetricsResponse struct {
	TriggerID       string `json:"trigger_id"`
	PendingCount    int    `json:"pending_count"`
	ClaimedCount    int    `json:"claimed_count"`
	SucceededCount  int    `json:"succeeded_count"`
	RetryCount      int    `json:"retry_count"`
	DeadLetterCount int    `json:"dead_letter_count"`
}

// --- Jobs (issue #1184 Workstream A) ----------------------------------------
// Mirrors the canonical Job DTOs in pkg/api/dto.go. Field tags + ordering
// + omitempty are part of the wire contract — the spec_compliance_test
// gate (TestSpecCompliance in cmd/apid) pins the OpenAPI schema's
// `required` arrays, so any drift between this file and pkg/api/dto.go
// breaks the gatewayd-public edge case.

// CreateJobRequest is the body of POST /v1/jobs.
type CreateJobRequest struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind,omitempty"`
	ImageRef       string            `json:"image_ref"`
	Command        []string          `json:"command"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
	RAMMB          int               `json:"ram_mb,omitempty"`
	TaskTimeoutSec int               `json:"task_timeout_sec,omitempty"`
	MaxParallelism int               `json:"max_parallelism,omitempty"`
	RetryMax       int               `json:"retry_max,omitempty"`
}

// UpdateJobRequest is the body of PATCH /v1/jobs/{name}.
type UpdateJobRequest struct {
	ImageRef       *string           `json:"image_ref,omitempty"`
	Command        []string          `json:"command,omitempty"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
	RAMMB          *int              `json:"ram_mb,omitempty"`
	TaskTimeoutSec *int              `json:"task_timeout_sec,omitempty"`
	MaxParallelism *int              `json:"max_parallelism,omitempty"`
	RetryMax       *int              `json:"retry_max,omitempty"`
	Status         *string           `json:"status,omitempty"`
}

// CreateJobRunRequest is the body of POST /v1/jobs/{name}/runs.
type CreateJobRunRequest struct {
	Tasks          int               `json:"tasks"`
	Parallelism    *int              `json:"parallelism,omitempty"`
	RetryMax       *int              `json:"retry_max,omitempty"`
	TaskTimeoutSec *int              `json:"task_timeout_sec,omitempty"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
}

// JobResponse is the wire projection of state.Job.
type JobResponse struct {
	ID             string            `json:"id"`
	AccountID      string            `json:"account_id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	ImageRef       string            `json:"image_ref"`
	Command        []string          `json:"command"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
	RAMMB          int               `json:"ram_mb"`
	TaskTimeoutSec int               `json:"task_timeout_sec"`
	MaxParallelism int               `json:"max_parallelism"`
	RetryMax       int               `json:"retry_max"`
	Status         string            `json:"status"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
}

// JobRunResponse is the wire projection of state.JobRun.
type JobRunResponse struct {
	ID              string            `json:"id"`
	JobID           string            `json:"job_id"`
	AccountID       string            `json:"account_id"`
	TriggerKind     string            `json:"trigger_kind"`
	EnvOverrides    map[string]string `json:"env_overrides,omitempty"`
	Tasks           int               `json:"tasks"`
	Parallelism     int               `json:"parallelism"`
	RetryMax        int               `json:"retry_max"`
	TaskTimeoutSec  int               `json:"task_timeout_sec"`
	AggregateStatus string            `json:"aggregate_status"`
	TasksSucceeded  int               `json:"tasks_succeeded"`
	TasksFailed     int               `json:"tasks_failed"`
	TasksCancelled  int               `json:"tasks_cancelled"`
	TasksRunning    int               `json:"tasks_running"`
	DeadLetterCount int               `json:"dead_letter_count"`
	StartedAt       string            `json:"started_at,omitempty"`
	FinishedAt      string            `json:"finished_at,omitempty"`
	CreatedAt       string            `json:"created_at"`
}

// JobTaskResponse is the wire projection of state.JobTask.
// LeaseToken is intentionally OMITTED (internal dispatch primitive).
type JobTaskResponse struct {
	RunID        string `json:"run_id"`
	TaskIndex    int    `json:"task_index"`
	Status       string `json:"status"`
	Attempt      int    `json:"attempt"`
	InstanceID   string `json:"instance_id,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// JobTaskLogResponse is the body of GET /v1/jobs/{name}/runs/{id}/
// tasks/{idx}/logs.
type JobTaskLogResponse struct {
	TaskStatus string `json:"task_status"`
	LogContent string `json:"log_content"`
	Truncated  bool   `json:"truncated"`
	MaxBytes   int    `json:"max_bytes"`
}

// ListJobsResponse is the body of GET /v1/jobs.
type ListJobsResponse struct {
	Jobs       []JobResponse `json:"jobs"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
	NextOffset int           `json:"next_offset"`
	Total      int           `json:"total"`
}

// ListJobRunsResponse is the body of GET /v1/jobs/{name}/runs.
type ListJobRunsResponse struct {
	Runs       []JobRunResponse `json:"runs"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	NextOffset int              `json:"next_offset"`
	Total      int              `json:"total"`
}

// ListJobTasksResponse is the body of GET /v1/jobs/{name}/runs/{id}/tasks.
type ListJobTasksResponse struct {
	Tasks      []JobTaskResponse `json:"tasks"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
	NextOffset int               `json:"next_offset"`
	Total      int               `json:"total"`
}

// JobRunCancelledResponse is the body of POST /v1/jobs/{name}/runs/{id}/cancel.
type JobRunCancelledResponse struct {
	Run         JobRunResponse `json:"run"`
	CancelledAt string         `json:"cancelled_at"`
}

// JobDeletedResponse is the body of DELETE /v1/jobs/{name}.
type JobDeletedResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
}

// CancelDeploymentRequest is the optional body of POST
// /v1/apps/{slug}/deployments/{id}/cancel. Reason must be one of
// the closed CancelReason values (empty → "user" server-side).
type CancelDeploymentRequest struct {
	Reason string `json:"reason,omitempty"`
}

// CancelDeploymentResponse is the wire shape for POST
// /v1/apps/{slug}/deployments/{id}/cancel. CancelledAt is the
// RFC3339 timestamp; CancelledBuilds is the list of
// cascade-cancelled build IDs (empty when no builds were
// in-flight).
type CancelDeploymentResponse struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	CancelledAt     time.Time `json:"cancelled_at"`
	CancelReason    string    `json:"cancel_reason"`
	CancelledBuilds []string  `json:"cancelled_builds"`
}

// ReorderDeploymentResponse is the wire shape for POST
// /v1/deployments/{id}/reorder. Priority is the server-applied
// value (echo of the request body after the row flip).
type ReorderDeploymentResponse struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
}

// ClearObsoleteReport is the response shape for POST
// /v1/apps/{slug}/deployments/clear-obsolete. Count is the
// number of soft-deleted rows in this call; OlderThan echoes the
// cutoff the store applied (default 168h).
type ClearObsoleteReport struct {
	AppSlug   string `json:"app_slug"`
	Count     int    `json:"count"`
	OlderThan string `json:"older_than"`
}

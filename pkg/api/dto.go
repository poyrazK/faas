package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

// Wire DTOs for the v1 REST API (spec Appendix A). Defined once here so apid and
// the faas CLI share exactly one contract; `--json` output stability (UX §3.2)
// depends on these shapes.

// CreateAppRequest creates an app or function.
type CreateAppRequest struct {
	Slug           string `json:"slug"`
	Type           string `json:"type,omitempty"`           // "app" (default) | "function"
	Runtime        string `json:"runtime,omitempty"`        // node22|python312|go124|go124-alpine|node24|python313 for functions
	RAMMB          int    `json:"ram_mb,omitempty"`         // 0 => plan default
	CPUMillicores  int    `json:"cpu_millicores,omitempty"` // 0 => 1000; allowed: 250, 500, 1000
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
	// Lifecycle settings are app-level defaults merged into every future
	// deployment manifest. Empty execution_mode/restart_policy and zero
	// deadline/retry values retain the mode/plan defaults. For service mode,
	// an omitted max_concurrency defaults to the requested desired replicas.
	ExecutionMode    string           `json:"execution_mode,omitempty"`
	RestartPolicy    string           `json:"restart_policy,omitempty"`
	StartupDeadlineS int              `json:"startup_deadline_s,omitempty"`
	MaxRetries       int              `json:"max_retries,omitempty"`
	ServiceReplicas  *ServiceReplicas `json:"service_replicas,omitempty"`
	// StreamingEnabled (issue #471) lets a customer opt out of
	// streaming at creation time. nil → plan default (Free off,
	// Hobby+ on). Explicit false on a Hobby/Pro/Scale plan = opt out
	// (a synchronous JSON API that wants Content-Length). Explicit
	// true on Free = rejected by apid with 403 plan_streaming_not_allowed.
	StreamingEnabled *bool `json:"streaming_enabled,omitempty"`
	// WarmSnapshotEnabled (issue #470 / ADR-055) opts the brand-new
	// app into the two-tier snapshot path (warm.snap on top of
	// init.snap). nil → plan default (Free/Hobby off; Pro/Scale on).
	// Explicit true on Free/Hobby = rejected by apid with 403
	// plan_warm_snapshot_not_allowed. Explicit false on Pro/Scale =
	// opt out (an app the customer knows will run cold every time).
	WarmSnapshotEnabled *bool `json:"warm_snapshot_enabled,omitempty"`
	// RequireAuthn (issue #560 / issue #695) opts the brand-new app into
	// per-deployment authentication. When true, gatewayd-internal
	// demands a valid Authorization: Bearer <token> header on every
	// routed request (the token must belong to the app's owning
	// account — cross-account tokens receive 403). nil → apid applies
	// the per-plan default (issue #695 / ADR-080): Free=false/"open",
	// Hobby=true/"open", Pro=true/"bearer", Scale=true/"bearer".
	// Explicit true on Free = rejected by apid with 403
	// plan_require_authn_not_allowed (the per-plan gate). Pointer
	// distinguishes "use the plan default" (nil) from "explicit false"
	// (opt out at create time, e.g. for a Hobby staging app that wants
	// the buffered public path) or "explicit true" (force-on for a
	// Free customer who is about to migrate to Hobby and wants to test
	// the path ahead of plan upgrade).
	RequireAuthn *bool `json:"require_authn,omitempty"`
	// WarmSnapshotMinRequests overrides the per-app request-count
	// threshold for warm-tier capture at creation time. nil → plan
	// default (5 on Pro/Scale; 0 on Free/Hobby). Range [1, 100].
	WarmSnapshotMinRequests *int `json:"warm_snapshot_min_requests,omitempty"`
	// WarmSnapshotMinMs overrides the per-app time-since-first-ready
	// threshold for warm-tier capture at creation time. nil → plan
	// default (2000 on Pro/Scale; 0 on Free/Hobby). Range [100, 60000].
	WarmSnapshotMinMs *int `json:"warm_snapshot_min_ms,omitempty"`
	// WebSocketEnabled (issue #676 / ADR-080) opts the brand-new
	// app into the raw-bytes Upgrade bridge (WebSocket / h2c /
	// MQTT-over-WS / long-poll). nil → plan default (Free off;
	// Hobby/Pro/Scale on). Explicit true on Free = rejected by
	// apid with 403 plan_websocket_not_allowed. Explicit false
	// on Hobby/Pro/Scale = opt out (a synchronous JSON API that
	// does not want long-poll connections pinning a wake past
	// wake_idle_timeout). The default-on shape mirrors the
	// streaming pattern from issue #471 — same fail-closed
	// contract, same Plan.WebSocketEnabled() accessor.
	WebSocketEnabled *bool `json:"websocket_enabled,omitempty"`
	// AppProtocol (ADR-124) is the per-app wire-protocol selector
	// (closed-set {http1, http2, grpc}). nil → apid applies the
	// universal default "http1" (Create) / "don't touch" (Update).
	// http2 is universally opt-in. grpc is Hobby/Pro/Scale only —
	// Free customers who set "grpc" are rejected by apid with 403
	// plan_app_protocol_grpc_not_allowed. Out-of-set values are
	// rejected with 400 app_protocol_invalid. See
	// pkg/api/limits.go::Plan.AppProtocolAllowed for the gate and
	// ADR-124 §Plan gating for the closed-set rationale.
	AppProtocol *string `json:"app_protocol,omitempty"`
	// RouteMetricsEnabled (ADR-093) opts the brand-new app into the
	// per-route observability surface (gatewayd-internal emits
	// `gateway_request_duration_seconds{app,route,class}` etc. plus
	// the bounded in-memory reader at
	// GET /v1/internal/apps/{slug}/routes). nil → plan default
	// (Free off; Hobby/Pro/Scale on). Explicit true on Free =
	// rejected by apid with 403 plan_route_metrics_not_allowed.
	// Explicit false on Hobby/Pro/Scale = opt out (a synchronous
	// JSON API that does not want per-route cardinality on the
	// box). The default-on shape mirrors the WebSocketEnabled
	// pattern from issue #676 — same fail-closed contract, same
	// Plan.RouteMetricsEnabled() accessor.
	RouteMetricsEnabled *bool `json:"route_metrics_enabled,omitempty"`
	// MaintenanceMode (ADR-091 amendment) opts the new app into
	// 503 + Retry-After mode at create time. The coarse sibling of
	// the kind=maintenance edge rule — the customer wants "this
	// whole app is in maintenance" without per-rule ceremony.
	// Free-tier allowed (no IsPaidOnly change); the per-app
	// apps_maintenance_mode_notify trigger (migration 00221)
	// fires pg_notify('app_changed', NEW.id) on a flip so the
	// gatewayd-internal apps LRU cache can be flushed.
	MaintenanceMode *bool `json:"maintenance_mode,omitempty"`
	// OverflowNode (Tier A10 / ADR-088) is the customer's per-app
	// preferred spill target for cross-node pressure rebalance.
	// Wire type is the human-readable compute_nodes.name; apid
	// resolves the name to the UUID server-side via
	// Store.ComputeNodeByName. nil at create time = "no
	// preference" (default A9 fallback). Empty-string "" at
	// create time is rejected with 422 invalid_overflow_node —
	// create-time has no "clear" path because the column starts
	// NULL.
	OverflowNode *string `json:"overflow_node,omitempty"`
}

// UpsertDevSessionRequest describes the application shape for an expiring,
// CLI-managed developer preview. The project identity lives in the URL path;
// WorkspaceID separates developers and local source trees within that project.
type UpsertDevSessionRequest struct {
	Type        string `json:"type,omitempty"`         // "app" (default) | "function"
	Runtime     string `json:"runtime,omitempty"`      // required for functions
	WorkspaceID string `json:"workspace_id,omitempty"` // opaque, CLI-derived local workspace identity
}

// DevSessionResponse is returned when a developer preview is created or its
// lease is refreshed. App.URL is the stable browser URL for this account and
// developer workspace; ExpiresAt is renewed whenever the CLI syncs source.
type DevSessionResponse struct {
	App       AppResponse `json:"app"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// UpdateAppRequest is the partial-update payload for PATCH /v1/apps/{slug}.
// All fields are pointers so the wire form can distinguish "not set" from
// "set to zero".
type UpdateAppRequest struct {
	RAMMB          *int `json:"ram_mb,omitempty"`
	CPUMillicores  *int `json:"cpu_millicores,omitempty"`
	IdleTimeoutS   *int `json:"idle_timeout_s,omitempty"`
	MaxConcurrency *int `json:"max_concurrency,omitempty"`
	// Lifecycle settings are partial updates. A non-nil service_replicas
	// replaces the full policy; use min=max=desired=0 to scale a service to
	// zero. desired must fit the app's max_concurrency; include both fields
	// when raising the target. Switching away from service clears the old
	// replica policy and drains live service replicas.
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
	// StreamingEnabled (issue #471) toggles the per-app streaming
	// response path through gatewayd-internal. When true (or unset on a plan
	// where the default is true), gatewayd-internal streams the response body
	// from the guest through to the client with a periodic 200 ms /
	// 256 KiB tx_bytes flush; when false, the legacy buffered path
	// runs (spec §4.1: 25 MB / 300 s). Plan-gated upstream: Free
	// returns 403 plan_streaming_not_allowed. Hobby/Pro/Scale may
	// PATCH true → false to opt out (e.g. a synchronous JSON API
	// that wants Content-Length). Pointer distinguishes "don't
	// touch" (nil) from "explicit false" (*bool=false).
	StreamingEnabled *bool `json:"streaming_enabled,omitempty"`
	// WebSocketEnabled (issue #676 / ADR-080) toggles the per-app
	// raw-bytes Upgrade bridge. When true (or unset on a plan
	// where the default is true), gatewayd-internal detects
	// Connection: Upgrade + Upgrade: <token> on inbound requests
	// and routes them via ForwardRawStream to the guest netns TCP
	// socket. When false, the upgrade detector returns 501 with
	// x-faas-error-reason: websocket_not_on_plan. Plan-gated
	// upstream: Free returns 403 plan_websocket_not_allowed when
	// a customer attempts PATCH true. Hobby/Pro/Scale may PATCH
	// true → false to opt out (e.g. a synchronous JSON API that
	// does not want long-poll pinning). Pointer distinguishes
	// "don't touch" (nil) from "explicit false" (*bool=false).
	WebSocketEnabled *bool `json:"websocket_enabled,omitempty"`
	// AppProtocol (ADR-124) is the per-app wire-protocol selector
	// (closed-set {http1, http2, grpc}). nil → apid applies the
	// universal default "http1" (Create) / "don't touch" (Update).
	// http2 is universally opt-in. grpc is Hobby/Pro/Scale only —
	// Free customers who set "grpc" are rejected by apid with 403
	// plan_app_protocol_grpc_not_allowed. Out-of-set values are
	// rejected with 400 app_protocol_invalid. See
	// pkg/api/limits.go::Plan.AppProtocolAllowed for the gate and
	// ADR-124 §Plan gating for the closed-set rationale.
	AppProtocol *string `json:"app_protocol,omitempty"`
	// RouteMetricsEnabled (ADR-093) toggles the per-app per-route
	// observability surface. When true (or unset on a plan where
	// the default is true), gatewayd-internal emits the per-route
	// Prometheus series and serves the per-app reader at
	// GET /v1/internal/apps/{slug}/routes. When false, the
	// routeLabelSet is empty for this app and the per-route series
	// do not appear on the /metrics scrape. Plan-gated upstream:
	// Free returns 403 plan_route_metrics_not_allowed when a
	// customer attempts PATCH true. Hobby/Pro/Scale may PATCH
	// true → false to opt out. Pointer distinguishes "don't
	// touch" (nil) from "explicit false" (*bool=false).
	RouteMetricsEnabled *bool `json:"route_metrics_enabled,omitempty"`
	// MaintenanceMode (ADR-091 amendment) opts the app into
	// 503 + Retry-After mode via PATCH. Pointer distinguishes
	// "don't touch" (nil) from "explicit false" (*bool=false).
	// Free-tier allowed (no IsPaidOnly change); the
	// apps_maintenance_mode_notify trigger fires pg_notify on
	// flip so the gatewayd-internal apps LRU cache stays fresh.
	MaintenanceMode *bool `json:"maintenance_mode,omitempty"`
	// RequireSigned (issue #472 / ADR-054) gates OCI image deploys on
	// a valid cosign signature from a trusted publisher (mirrors AWS
	// Lambda's Code Signing for Lambda). When true, imaged verifies
	// the deploy image against the per-app trusted-publisher list
	// (PUT /v1/apps/{slug}/trusted_signers/{name}) before any
	// buildImageLayer work. Default false — pre-PR apps stay on the
	// open-deploy path. Pointer distinguishes "don't touch" (nil)
	// from "explicit false" (opt out). Admin-only via PATCH
	// /v1/apps/{slug}; not plan-gated (any plan may opt in).
	// Source-tarball deploys (Railpack path) bypass the gate by
	// design — builds run inside ephemeral builder microVMs
	// (ADR-003) and the customer image is never shipped over the wire.
	RequireSigned *bool `json:"require_signed,omitempty"`
	// WarmSnapshotEnabled (issue #470 / ADR-055) toggles the
	// two-tier snapshot path on an existing app. Pointer
	// distinguishes "don't touch" (nil) from "explicit false"
	// (opt out of warm capture). Plan-gated upstream: Free/Hobby
	// + true returns 403 plan_warm_snapshot_not_allowed. The
	// Pro/Scale default (on) applies to brand-new apps at
	// CreateApp time; this PATCH lets a customer flip it on for
	// a Free/Hobby app they later upgrade, or off for a Pro app
	// they know runs cold every time.
	WarmSnapshotEnabled *bool `json:"warm_snapshot_enabled,omitempty"`
	// RequireAuthn (issue #560) toggles per-deployment authentication
	// on an existing app. Pointer distinguishes "don't touch"
	// (nil) from "explicit false" (opt out — back to the public
	// path). Plan-gated upstream: Free/Hobby + true returns 403
	// plan_require_authn_not_allowed. The default (false) applies
	// to brand-new apps at CreateApp time regardless of plan, so
	// every existing customer stays public-by-default. Token
	// scope is enforced by gatewayd-internal's authz branch, not
	// here: a valid token from a different account receives 403
	// at the gateway, not at the PATCH endpoint.
	RequireAuthn *bool `json:"require_authn,omitempty"`
	// PublicAuth (issue #477 / ADR-079) toggles per-app
	// public-URL auth (open|bearer|basic). nil = don't
	// touch the column (pre-#477 behaviour preserved).
	// Non-nil with the SetPublicAuth bit set replaces the
	// apps row's public_auth_mode + public_auth_basic
	// columns atomically. Plan-gated upstream:
	// bearer=Hobby+, basic=Pro+; the apid validator
	// returns 402 plan_public_auth_{bearer,basic}_not_allowed
	// when the customer's plan lacks the gate. On the
	// 'open' / 'bearer' shapes, the BasicUser/BasicPass
	// fields are ignored and any existing sealed blob is
	// cleared; on 'basic' the apid seal step writes a new
	// secretbox-sealed APP_BASIC_AUTH blob carrying
	// {basic_user, basic_pass} and gatewayd-internal
	// unseals at boot (60s cache + db.NotifyKeyChanged
	// invalidation on the hot path).
	PublicAuth    *PublicAuthBlock `json:"public_auth,omitempty"`
	SetPublicAuth bool             `json:"-"`
	// WarmSnapshotMinRequests overrides the per-app request-count
	// threshold for warm-tier capture. nil → keep current value
	// (or apply the plan default on a future create). Range
	// [1, 100] — out-of-range values return 422
	// invalid_warm_snapshot_min_requests.
	WarmSnapshotMinRequests *int `json:"warm_snapshot_min_requests,omitempty"`
	// WarmSnapshotMinMs overrides the per-app time-since-first-ready
	// threshold for warm-tier capture. nil → keep current value.
	// Range [100, 60000] — out-of-range values return 422
	// invalid_warm_snapshot_min_ms.
	WarmSnapshotMinMs *int `json:"warm_snapshot_min_ms,omitempty"`
	// EvictionPriority (issue #475) classifies the app under
	// cross-account RAM pressure. Values: 'best_effort' (default,
	// pre-#475 behaviour) or 'reserved' (opt-in protected tier).
	// The plan gate is enforced in apid — Free PATCH 'reserved'
	// returns 403 plan_eviction_priority_reserved_not_allowed.
	// The per-account cap (Plan.ReservedConcurrencyPerAccount)
	// returns 422 plan_eviction_priority_reserved_quota. nil → keep
	// current value (or apply the schema default 'best_effort' on
	// a future create). Flipping between the two values is
	// always allowed (any plan may go in either direction once
	// the reserved tier is unlocked) — the cap is over APPS, not
	// instances, so flipping down always frees a slot.
	EvictionPriority *string `json:"eviction_priority,omitempty"`
	// OverflowNode (Tier A10 / ADR-088) is the customer's per-app
	// preferred spill target for cross-node pressure rebalance.
	// Tri-state: nil = no change, "" = clear (back to A9 default
	// fallback), non-empty = resolve server-side (404 on unknown
	// name → 422 invalid_overflow_node; 422 on inactive node).
	// Resolution is `Store.ComputeNodeByName(name)` → the FK on
	// apps.overflow_node (migration 00167). Engine consults the
	// resolved UUID on the next pressured sweep; falls through to
	// A9 if the peer has no headroom or is inactive.
	OverflowNode *string `json:"overflow_node,omitempty"`
	// CORSDefaultEnabled (CORS improvements D1 / ADR-091
	// appendix) opts the app into the soft default-CORS
	// fallback. When true, every incoming response is
	// stamped with a CORS header set derived from
	// CORSDefaultOrigins whenever no explicit kind=cors
	// edge rule matches. nil → keep current value
	// (existing PATCH semantics); explicit false opts out;
	// explicit true opts in. The Set-bit in the
	// pgstore-level UpdateApp distinguishes "unset" from
	// "explicit flip" so partial-PATCH stays bit-for-bit
	// additive on the wire.
	CORSDefaultEnabled *bool `json:"cors_default_enabled,omitempty"`
	// CORSDefaultOrigins (CORS improvements D1) is the
	// per-app default allowlist. Same grammar as
	// EdgeRuleCORSAction.AllowOrigins — concrete origins,
	// '*.example.com' subdomain wildcards, 'localhost:*'
	// port wildcards, or '*' (denied when credentials
	// are enabled, same footgun guard as the explicit
	// rule). nil slice or len==0 means "no default
	// fallback"; when CORSDefaultEnabled is true the
	// validator requires a non-empty value (otherwise
	// the fallback is a silent no-op and the customer
	// wonders why nothing changed). Stamped on the
	// gateway hot path by reusing pkg/gateway's
	// matchOrigin predicate against this list.
	CORSDefaultOrigins *[]string `json:"cors_default_origins,omitempty"`
	// RootDir, WorkloadName, StartCommand mirror the apps table
	// columns added in Phase 1 (migration 00074). The customer-facing
	// PATCH handler (cmd/apid/handlers_ext.go) ignores them today —
	// they're populated by pkg/reconcile in PR-G/H via the internal
	// updateApp flow. json tags keep them off the customer wire
	// surface (`omitempty` on every field, no separate routes
	// targeting these) so an existing CLI SDK never sends them.
	RootDir      *string `json:"-"`
	WorkloadName *string `json:"-"`
	StartCommand *string `json:""`
	// ScalingPolicy is the per-app autoscaling configuration
	// (issue #462 / ADR-058). nil pointer = "don't touch the
	// jsonb column"; non-nil with the Set bit set = "replace the
	// jsonb column with this shape". The DTO uses value semantics
	// (not pointer-to-int) so the wire form can omit fields
	// ("scale_out_cooldown_s: 0" = "set to the engine default")
	// without a three-state "unset / zero / explicit" dance.
	// SetScalingPolicy (parallel to SetMinInstances above)
	// distinguishes "don't touch" (nil pointer) from "explicit
	// zero policy" (non-nil struct with zero fields = "scale to
	// zero, the v1 contract"). Plan-gated upstream:
	//   MinInstances > 0   → Hobby+ (was Pro/Scale, PR-A
	//                              tier-up)
	//   MaxInstances > 0   → Hobby+ (new gate at PR-A)
	//   Target.Metric != "" → Hobby+ (PR-A ships the DTO; PR-C
	//                              wires the engine)
	//   Worker-class concurrent_requests → 422
	//                              (scaling_target_incompatible_with_
	//                              workload_class, PR-D carve-out)
	// Cooldowns clamped to package constants
	// (`MinScaleOutCooldownS` / `MaxScaleOutCooldownS` /
	// `MinScaleInCooldownS` / `MaxScaleInCooldownS`).
	ScalingPolicy    *ScalingPolicy `json:"scaling_policy,omitempty"`
	SetScalingPolicy bool           `json:"-"`
}

// RenameAppRequest is the body of POST /v1/apps/{slug}/rename (issue #63).
// Validated server-side via the same validSlug regex used at CreateApp
// time; rejected on conflict with 409 CodeAppRenameFailed when another
// live app already holds NewSlug.
type RenameAppRequest struct {
	NewSlug string `json:"new_slug"`
}

// ScalingPolicy is the per-app autoscaling configuration wire shape
// (issue #462 / ADR-058). Mirrors the on-disk jsonb column
// `apps.scaling_policy` and the in-memory `state.ScalingPolicy`.
// Empty values map to the engine default (the apid gate is what
// enforces the floor / ceiling, not the encoder).
//
// Field semantics:
//
//	MinInstances: per-app cold-wake floor. 0 = scale to zero (the
//	  pre-#462 default). Hobby+ unlocked at PR-A time (was Pro/Scale).
//	MaxInstances: per-app ceiling on live instances. Must be in
//	  [MinInstances, plan.MaxConcurrency]. Hobby+ unlocked at PR-A
//	  time. 0 = "use plan max_concurrency".
//	Target: the per-instance signal the engine watches for the
//	  scale-up trigger. Closed metric set: "rps" |
//	  "concurrent_requests" | "p99_latency_ms". Empty Metric =
//	  "disabled" (the engine falls back to the legacy
//	  autoscale_target_rps / autoscale_target_cpu_pct columns).
//	ScaleOutCooldownS: minimum seconds between two scale-out
//	  events. Floor 1 (no `0` traps); ceiling 3600 (1 h).
//	ScaleInCooldownS: minimum seconds between two scale-in events.
//	  Floor 5 (longer than the scale-out floor to dampen
//	  oscillation); ceiling 86400 (1 day).
//
// The DTO uses value semantics (no inner pointer fields) so the
// wire allows `{...}` (zero-value policy) for the "scale to zero"
// semantics. The handler rejects the JSON `null` value via strict
// UnmarshalJSON.
type ScalingPolicy struct {
	MinInstances      int            `json:"min_instances,omitempty"`
	MaxInstances      int            `json:"max_instances,omitempty"`
	Target            *ScalingTarget `json:"target,omitempty"`
	ScaleOutCooldownS int            `json:"scale_out_cooldown_s,omitempty"`
	ScaleInCooldownS  int            `json:"scale_in_cooldown_s,omitempty"`
	// unknownFields is the set of unknown JSON keys encountered
	// during a strict Unmarshal. Stored as a one-shot value so
	// the validator can surface a single error without
	// re-implementing the duck-typed reject. Cleared on the next
	// successful unmarshal.
	unknownFields []string `json:"-"`
}

// ScalingTarget is the (metric, value) pair the engine watches for
// the scale-up trigger. The metric surface is closed. The unset
// state (nil pointer) is the legacy "engine falls back to
// autoscale_target_rps / autoscale_target_cpu_pct" path.
type ScalingTarget struct {
	Metric string  `json:"metric,omitempty"`
	Value  float64 `json:"value,omitempty"`
}

// UnmarshalJSON implements a strict decoder for ScalingPolicy:
// unknown fields are rejected with a synthetic CodeValidation
// problem so the wire surface stays self-documenting. Mirrors the
// EgressAllowlist DTO shape (handler-side strict surface).
//
// Single decode: a json.Decoder with DisallowUnknownFields gives us
// "unknown field" rejection for free, and the alias-copy preserves
// the unexported unknownFields list because the alias type does NOT
// inherit it. We capture unknown fields BEFORE the decode (via the
// decoder's field-name hook) so the validator can attach them to
// the *Problem. The alias technique drops unknownFields on the
// post-decode assignment; we restore it on the local-then-receiver
// copy in one line.
func (s *ScalingPolicy) UnmarshalJSON(data []byte) error {
	// Walk the raw object ONCE to learn which keys appear; this
	// runs alongside the typed decode below (json/Decoder tokenises
	// the input twice but we only marshal the bytes once — the
	// alternative json.Unmarshal+json.Unmarshal paid twice the cost
	// we now avoid by deferring the typed decode to the alias).
	allowed := map[string]struct{}{
		"min_instances":        {},
		"max_instances":        {},
		"target":               {},
		"scale_out_cooldown_s": {},
		"scale_in_cooldown_s":  {},
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("scaling_policy: %w", err)
	}
	unknown := make([]string, 0)
	for k := range raw {
		if _, ok := allowed[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	// Decode the typed shape with DisallowUnknownFields so a stray
	// key produces a hard error rather than a silent drop. We
	// recover the unknown-fields list locally and re-attach it
	// after the alias copy below (the alias type lacks the
	// unexported field, so the assignment would zero it; we
	// re-attach via `*s = ScalingPolicy(a); s.unknownFields = ...`
	// in one statement to make the data flow obvious).
	type alias ScalingPolicy
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("scaling_policy: %w", err)
	}
	*s = ScalingPolicy(a)
	s.unknownFields = unknown
	return nil
}

// HasUnknownFields reports whether the most recent UnmarshalJSON
// encountered any unknown fields. Cleared by a successful
// Validator pass that consumes the list.
func (s *ScalingPolicy) HasUnknownFields() bool {
	return len(s.unknownFields) > 0
}

// UnknownFields returns the sorted list of unknown-field names seen
// during the most recent UnmarshalJSON. Empty after a clean
// decode.
func (s *ScalingPolicy) UnknownFields() []string {
	return s.unknownFields
}

// ClearUnknownFields drops the stored unknown-fields list. Called
// by the validator after the *Problem is built, so a successful
// Validate → PATCH flow doesn't leak the previous decode's list
// into a subsequent re-validation.
func (s *ScalingPolicy) ClearUnknownFields() {
	s.unknownFields = nil
}

// AppEffectiveLimits is the resource and edge envelope that actually applies
// to an app. It gives customers one place to see plan-derived limits whose
// names otherwise invite incorrect assumptions (for example, guest-visible
// vCPUs are distinct from the sustained cgroup CPU allowance).
type AppEffectiveLimits struct {
	MemoryLimitMB          int   `json:"memory_limit_mb"`
	PlanMemoryMaxMB        int   `json:"plan_memory_max_mb"`
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

// AppConfiguredResources is the resource shape selected for an app. It is
// separate from EffectiveLimits because guest topology and plan ceilings can
// differ from the app's sustained CPU and memory settings.
type AppConfiguredResources struct {
	MemoryMB      int `json:"memory_mb"`
	CPUMillicores int `json:"cpu_millicores"`
}

// AppResponse is an app as returned by the API.
type AppResponse struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Type           string `json:"type"`
	Runtime        string `json:"runtime,omitempty"`
	RAMMB          int    `json:"ram_mb"`
	CPUMillicores  int    `json:"cpu_millicores"`
	MaxConcurrency int    `json:"max_concurrency"`
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
	ConcurrencyPerVMBound int `json:"concurrency_per_vm"`
	// EffectiveLimits makes the complete resource and request envelope
	// visible without requiring the customer to infer it from their plan.
	EffectiveLimits     AppEffectiveLimits     `json:"effective_limits"`
	ConfiguredResources AppConfiguredResources `json:"configured_resources"`
	IdleTimeoutS        int                    `json:"idle_timeout_s,omitempty"`
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
	// StreamingEnabled (issue #471) reflects the per-app flag stored
	// on the apps row. False on Free (the plan default and the only
	// legal state on Free — apid rejects PATCH true with 403
	// plan_streaming_not_allowed). True on Hobby/Pro/Scale by default
	// unless the customer explicitly opted out via PATCH. Surfaced so
	// dashboards can show "streaming on / off" alongside the
	// egress-allowlist flag.
	StreamingEnabled bool `json:"streaming_enabled"`
	// WebSocketEnabled (issue #676 / ADR-080) reflects the per-app
	// raw-bytes Upgrade bridge flag stored on the apps row. False
	// on Free (the plan default and the only legal state — apid
	// rejects PATCH true with 403 plan_websocket_not_allowed).
	// True on Hobby/Pro/Scale by default unless the customer
	// explicitly opted out via PATCH. Surfaced so dashboards can
	// show "websocket on / off" alongside the streaming pill.
	WebSocketEnabled bool `json:"websocket_enabled"`
	// AppProtocol (ADR-124) is the wire-protocol selector stored on
	// the apps row. Always "http1" on a Free-or-above app that
	// didn't set the field — the universal default. Set to "http2"
	// or "grpc" by the customer via PATCH /v1/apps/{slug} or
	// manifest. Surfaced so dashboards can show a protocol pill
	// alongside the streaming/WS pills; the column is NOT NULL
	// DEFAULT 'http1' in schema.sql so the empty-string fallback
	// is impossible.
	AppProtocol string `json:"app_protocol"`
	// RouteMetricsEnabled (ADR-093) reflects the per-app
	// route_metrics_enabled flag stored on the apps row. False
	// on Free (the plan default and the only legal state — apid
	// rejects PATCH true with 403 plan_route_metrics_not_allowed).
	// True on Hobby/Pro/Scale by default unless the customer
	// explicitly opted out via PATCH. Surfaced so dashboards can
	// show "route metrics on / off" alongside the streaming/WS
	// pills.
	RouteMetricsEnabled bool `json:"route_metrics_enabled"`
	// MaintenanceMode (ADR-091 amendment) is the coarse-grained
	// maintenance toggle for the whole app. When true the
	// gatewayd applier (applyAppsMaintenanceMode, §4.1.2.0)
	// short-circuits every request with 503 + Retry-After
	// BEFORE auth, BEFORE wake, BEFORE any kind=maintenance rule
	// (coarse gate beats fine-grained). Surfaced in the GET app
	// response so dashboards can show "maintenance on / off".
	MaintenanceMode bool `json:"maintenance_mode"`
	// EvictionPriority (issue #475) is the per-app eviction tier
	// classification. 'best_effort' (default for every pre-#475
	// row, applied by the column DEFAULT at migration time) keeps
	// the historical LRU-by-last_request_at reaper behaviour;
	// 'reserved' (Hobby+ only, per-account cap enforced) protects
	// the app from cross-account RAM-pressure eviction. Surfaces
	// in `gregale app <slug>` text output and the JSON view so
	// customers can verify their PATCH round-tripped.
	EvictionPriority string `json:"eviction_priority"`
	// ScalingPolicy is the per-app autoscaling configuration (issue
	// #462 / ADR-058). The struct is the wire DTO for the on-disk
	// jsonb column `apps.scaling_policy`; the in-memory state type
	// (`state.ScalingPolicy`) is the canonical source. Materialised
	// as `null` for legacy rows (applies the empty-policy
	// projection = "use min_instances / max_concurrency from the
	// existing columns"). Dashboards branch on `null` to render the
	// "default scale-to-zero" pill. The full nested shape mirrors
	// the issue-body example:
	//
	//   {"min_instances": 0,
	//    "max_instances": 5,
	//    "target": {"metric": "concurrent_requests", "value": 1.0},
	//    "scale_out_cooldown_s": 5,
	//    "scale_in_cooldown_s": 60}
	//
	// Plan-gated upstream: Hobby+ unlocks MinInstancesAllowed at
	// PR-A time (was Pro/Scale); MaxInstancesAllowed tier-up is
	// a Hobby+ gate at the same time. Worker-class apps reject
	// `target.metric = concurrent_requests` with 422
	// `scaling_target_incompatible_with_workload_class` (PR-D
	// carve-out).
	ScalingPolicy *ScalingPolicy `json:"scaling_policy,omitempty"`
	// LastScaleOutAt / LastScaleInAt are the wall-clock timestamps
	// schedd stamps on the wake-gate admit / reaper park branches
	// (issue #462 / ADR-058). Used by the cooldown helper to
	// short-circuit requests inside the cooldown window. Surfaced
	// on GET so dashboards can render "warm-up in progress" +
	// "recently scaled" copy. RFC 3339 string form; nil on a
	// never-scaled app.
	LastScaleOutAt *time.Time `json:"last_scale_out_at,omitempty"`
	LastScaleInAt  *time.Time `json:"last_scale_in_at,omitempty"`
	// RequireSigned (issue #472 / ADR-054) reflects the per-app
	// signature-enforcement flag. False by default; toggled true via
	// PATCH /v1/apps/{slug}. When true, OCI image deploys are
	// rejected at imaged's verify hook (403 deploy_signature_invalid)
	// unless the image carries a cosign signature from one of the
	// per-app trusted publishers (GET /v1/apps/{slug}/trusted_signers).
	// Source-tarball deploys are unaffected.
	RequireSigned bool `json:"require_signed"`
	// WarmSnapshotEnabled (issue #470 / ADR-055) reflects the
	// per-app two-tier-snapshot flag. False on Free/Hobby (the
	// plan default and the only legal state — apid rejects
	// PATCH-true with 403 plan_warm_snapshot_not_allowed). True
	// on Pro/Scale unless the customer explicitly opted out via
	// PATCH. Surfaced so dashboards can show "warm snapshot on /
	// off" alongside the streaming + require_signed pills.
	WarmSnapshotEnabled bool `json:"warm_snapshot_enabled"`
	// RequireAuthn (issue #560) reflects the per-deployment
	// authentication flag. False by default on every plan (the
	// column default + the per-plan default — the gate only
	// fires when a Free/Hobby customer tries to PATCH true,
	// which is denied). When true on a Pro/Scale app,
	// gatewayd-internal demands a valid bearer token on every
	// request (the token must belong to the app's owning
	// account — cross-account tokens receive 403). Surfaced so
	// dashboards can show the "auth required" pill alongside
	// streaming / warm-snapshot / require_signed.
	RequireAuthn bool `json:"require_authn"`
	// PublicAuth (issue #477 / ADR-079) reflects the
	// per-app public-URL auth mode. Three shapes:
	//   {mode:"open"}    — pre-#477 default; every existing
	//                      app stays public-by-default.
	//   {mode:"bearer"}  — gatewayd-internal demands an
	//                      Authorization: Bearer header
	//                      (re-uses the require_authn chain).
	//                      Available Hobby+ only.
	//   {mode:"basic"}   — gatewayd-internal demands an
	//                      Authorization: Basic header and
	//                      verifies against the sealed
	//                      APP_BASIC_AUTH blob. Available
	//                      Pro+ only. HasBasicCreds is true
	//                      on the response so dashboards
	//                      know whether creds are currently
	//                      configured (the plaintext is
	//                      NEVER echoed — it lives in
	//                      app_secrets, ADR-045).
	PublicAuth PublicAuthStatus `json:"public_auth"`
	// AuthDefaultFlippedAt (issue #695 / ADR-080) is the
	// grand-father marker for the apps-auth-default flip. Set
	// on apps that pre-date the global flip (migration 00155
	// stamped every pre-flip row at migration time); null on
	// apps created after the flip — no grand-father needed for
	// fresh inserts because the per-plan default was already
	// applied at create time via
	// Plan.RequireAuthnDefault() + Plan.PublicAuthModeDefault().
	// Read-only — the PATCH side has no field to mutate this,
	// and a future contributor adding one must refuse it with
	// 422 unprocessable_entity per ADR-080 §9. Dashboards
	// render the "AUTH: <mode>" annotation with the "since
	// YYYY-MM-DD" suffix only when this is non-null.
	AuthDefaultFlippedAt *time.Time `json:"auth_default_flipped_at,omitempty"`
	// WarmSnapshotMinRequests / WarmSnapshotMinMs surface the
	// per-app capture thresholds. Range [1, 100] and [100, 60000]
	// respectively; out-of-range PATCH values are rejected at
	// the apid handler before they reach the store.
	WarmSnapshotMinRequests int `json:"warm_snapshot_min_requests"`
	WarmSnapshotMinMs       int `json:"warm_snapshot_min_ms"`
	// ParkedDeployment (issue #554 / ADR-079 follow-up) is the
	// most-recently parked deployment for this app, or nil if the
	// app has never been parked. Powers the "why is my app
	// evicted_cold?" UX surface — operators see the closed-set
	// reason (liveness_exhausted | lifecycle_park | admin_park) +
	// the timestamp without grepping the audit log. Nested (not
	// flat) so AppResponse conflates app-state with
	// deployment-state only at the explicit ref — mirrors the
	// per-deployment override pattern at DeploymentResponse.
	ParkedDeployment *ParkedDeploymentRef `json:"parked_deployment,omitempty"`
	// OverflowNode (Tier A10 / ADR-088) echoes the resolved UUID
	// of the customer's per-app preferred spill target. NULL
	// when no preference is set (the default A9 fallback).
	// Dashboards branch on `null` to render the "no spill
	// target" pill. The wire DTO is UUID-shaped (not name) so
	// the value is unambiguous across operator-deployed fleets
	// with non-unique names — apid always resolves the wire
	// `name` to a `compute_nodes.id` server-side before
	// persisting or returning.
	OverflowNode *string `json:"overflow_node,omitempty"`
	// CORSDefaultEnabled (CORS improvements D1) reflects the
	// per-app soft default-CORS opt-in. NULL on legacy rows
	// so dashboards render an "off" pill; non-null true
	// means the gateway is stamping the default header set
	// on miss; non-null false means the customer explicitly
	// opted out (the column was flipped from the schema
	// default).
	CORSDefaultEnabled *bool `json:"cors_default_enabled"`
	// CORSDefaultOrigins (CORS improvements D1) reflects the
	// resolved default allowlist. nil/len==0 means the
	// column is NULL (no origins configured); the gateway
	// short-circuits to a deny-all stamp on the default
	// path. The wire shape is JSON [] (never null) when
	// the column carries a value, so dashboards can render
	// the list directly.
	CORSDefaultOrigins []string `json:"cors_default_origins"`
}

// ParkedDeploymentRef is the reference shape returned in
// AppResponse.ParkedDeployment (issue #554 / ADR-079 follow-up).
// Lives in pkg/api/dto.go per pkg-api-cannot-import-pkg-state so
// the wire DTO does not pull in the state package. omitempty on
// the pointer field handles the "app has never been parked"
// branch (no field on the wire).
type ParkedDeploymentRef struct {
	ID           string     `json:"id"`
	ParkedReason string     `json:"parked_reason"`
	ParkedAt     *time.Time `json:"parked_at"`
}

// PublicAuthBlock (issue #477 / ADR-079 + ADR-118 + ADR-119)
// is the per-app public-URL auth configuration on a PATCH body.
// Mode is the canonical 'open'|'bearer'|'basic'|'ip_allowlist'|
// 'internal_only' string (must match apps_public_auth_mode_chk).
// BasicUser + BasicPass are only meaningful when
// Mode='basic'; the apid PATCH handler seals them under
// the APP_BASIC_AUTH secretbox namespace and stores the
// ciphertext in apps.public_auth_basic. For Mode='open',
// 'bearer', 'ip_allowlist', or 'internal_only' the apid
// handler ignores them (and clears any existing sealed blob
// so a stale secretbox row never reaches a fresh request).
// For Mode='ip_allowlist' the IPAllowlist slice must be
// non-empty (ADR-118; the 500-on-misconfig gate in
// pkg/gateway/handler.go fires otherwise). For
// Mode='internal_only' (ADR-119) the JWT lives on the
// request, not the app row — no app-side configuration is
// required beyond the mode flip; the per-service public-key
// allowlist is operator-side (FAAS_INTERNAL_SVC_PUBKEYS on
// gatewayd-internal). The wire-shape reflects what the
// customer PATCHes; the on-disk shape is the
// public_auth_mode + public_auth_basic +
// public_auth_ip_allowlist columns plus the secretbox
// seal at PATCH time.
type PublicAuthBlock struct {
	// Mode is the canonical 'open'|'bearer'|'basic'|
	// 'ip_allowlist'|'internal_only' string. apid rejects
	// unknown values with 422 invalid_public_auth_mode.
	Mode string `json:"mode"`
	// BasicUser is the basic-auth username (plaintext at
	// PATCH time; sealed before persist). Required when
	// Mode='basic'; ignored otherwise. Range
	// [1, 128] bytes after TrimSpace.
	BasicUser string `json:"basic_user,omitempty"`
	// BasicPass is the basic-auth password (plaintext at
	// PATCH time; sealed before persist). Required when
	// Mode='basic'; ignored otherwise. Range
	// [1, 256] bytes.
	BasicPass string `json:"basic_pass,omitempty"`
	// IPAllowlist (ADR-118) is the per-app ingress CIDR
	// allowlist. Required when Mode='ip_allowlist';
	// ignored otherwise. Plan-gated to Pro+ (apid
	// returns 403 plan_public_auth_ip_allowlist_not_allowed
	// for Free/Hobby). Per-plan cap
	// (pkg/api/limits.go::PublicAuthIPAllowlistMaxEntries)
	// is enforced upstream of this Validate. The apid
	// parse step rejects entries that don't net.ParseCIDR
	// or that have masklen=0 (the same non-/0 contract
	// the DB trigger at migrations/00308 enforces).
	IPAllowlist []string `json:"ip_allowlist,omitempty"`
}

// Validate enforces the canonical PublicAuthBlock shape:
// Mode is a closed enum; BasicUser + BasicPass are
// required iff Mode='basic'; IPAllowlist must be
// non-empty iff Mode='ip_allowlist'. Returns a 422-mapped
// *Problem on any malformed shape. nil in → nil out
// (the caller treats nil as "don't touch the column").
//
// ADR-119 added 'internal_only'. internal_only requires no
// app-side payload — the JWT lives on the request. Validate
// simply accepts the mode without checking further fields
// (the gate at pkg/gateway/handler.go::applyIngressInternalSvc
// handles the auth; the synth handler gate at
// pkg/gateway/synth.go::handleSynthesize handles the cron path).
func (b *PublicAuthBlock) Validate() *Problem {
	if b == nil {
		return nil
	}
	switch b.Mode {
	case AppPublicAuthModeOpen, AppPublicAuthModeBearer, AppPublicAuthModeBasic,
		AppPublicAuthModeIPAllowlist, AppPublicAuthModeInternalOnly:
	default:
		return NewProblem(422, CodeValidation, "Invalid public_auth.mode",
			fmt.Sprintf("public_auth.mode must be 'open', 'bearer', 'basic', 'ip_allowlist', or 'internal_only'; got %q", b.Mode))
	}
	if b.Mode != AppPublicAuthModeBasic {
		// IPAllowlist is required iff Mode='ip_allowlist'.
		// The plan-gate + size cap + per-entry parse are
		// enforced upstream of Validate (the apid handler
		// runs them in that order: closed-enum → plan → size
		// → parse), so this layer only checks the structural
		// "non-empty when mode requires it" invariant.
		if b.Mode == AppPublicAuthModeIPAllowlist && len(b.IPAllowlist) == 0 {
			return NewProblem(422, CodeValidation, "Invalid public_auth.ip_allowlist",
				"public_auth.ip_allowlist must contain at least one CIDR when mode='ip_allowlist'")
		}
		return nil
	}
	if u := strings.TrimSpace(b.BasicUser); u == "" || len(u) > AppPublicAuthBasicUserMaxBytes {
		return NewProblem(422, CodeValidation, "Invalid public_auth.basic_user",
			fmt.Sprintf("public_auth.basic_user must be 1..%d bytes when mode='basic'", AppPublicAuthBasicUserMaxBytes))
	}
	if p := strings.TrimSpace(b.BasicPass); p == "" || len(p) > AppPublicAuthBasicPassMaxBytes {
		return NewProblem(422, CodeValidation, "Invalid public_auth.basic_pass",
			fmt.Sprintf("public_auth.basic_pass must be 1..%d bytes when mode='basic'", AppPublicAuthBasicPassMaxBytes))
	}
	return nil
}

// PublicAuthStatus (issue #477 / ADR-079 + ADR-118) is
// the read-only per-app public-URL auth surface on
// AppResponse. Mode mirrors the apps.public_auth_mode
// column; HasBasicCreds is true iff the row has a
// non-null public_auth_basic blob (a mode='basic' app
// without creds would still 401 every request).
// IPAllowlistEntryCount is the count of CIDRs in the
// apps.public_auth_ip_allowlist array (mode='ip_allowlist'
// apps only — 0 otherwise). The CIDR strings themselves
// are NEVER echoed — they're operator-secret and an
// operator triaging "why am I getting 403s" can read the
// raw value from the apid audit log instead (the audit
// log records the count, not the entries — redaction
// invariant). The plaintext basic-auth username/password
// is NEVER echoed — it lives in app_secrets (ADR-045)
// and is loopback-mounted to drive1 at boot.
type PublicAuthStatus struct {
	Mode                  string `json:"mode"`
	HasBasicCreds         bool   `json:"has_basic_creds"`
	IPAllowlistEntryCount int    `json:"ip_allowlist_entry_count"`
}

// Sidecars is the array shape on `CreateDeploymentRequest.Sidecars`
// (issue #463 / ADR-068). Defined as a named slice so callers can
// pin the `Validate(limits)` method (Go does not allow defining
// methods on `[]T` directly, but `type T []Foo` makes the method
// attach to the named alias). See Sidecar / Sidecars.Validate
// below for the contract.
type Sidecars []Sidecar

// CreateDeploymentRequest ships a version (JSON variant; the multipart
// variant is used for tarball/dockerfile deploys).
type CreateDeploymentRequest struct {
	Image string `json:"image,omitempty"` // registry.gregale.dev/...@sha256:...
	// Overrides is the Fargate-shaped deploy-time override object
	// (issue #460 / ADR-053). Lets a customer redeploy the same
	// digest-pinned image with a different entrypoint/cmd/env/port
	// without rebuilding the image. The field list is frozen by
	// ADR-053 §Decision 1 — any new override field requires a new
	// ADR. Nil/omitted means "no overrides; deploy the image as-is".
	Overrides *CreateDeploymentOverrides `json:"overrides,omitempty"`
	// RequireSigned (issue #472 / ADR-054) is the per-deploy opt-in
	// to cosign signature verification. apid flips the row flag from
	// the request body and imaged's buildImageLayer verifies before
	// PullDigest. The operator policy on apps.require_signed is the
	// source of truth — a customer request that tries to clear an
	// operator-on flag is rejected (operator > customer). nil on the
	// wire = leave the per-app flag alone (default off). Source-
	// tarball deploys ignore this field (Railpack path bypasses the
	// verify hook entirely).
	RequireSigned *bool `json:"require_signed,omitempty"`
	// Sidecars (issue #463 / ADR-068) attaches up to 2 stateless
	// sidecars (1 init + 1 sidecar) to the deployment. nil/empty
	// = no sidecars. PR-A persists the field; PR-B wires the
	// runtime effect (imaged + fcvm + guest-init + cgroup); PR-C
	// wires e2e + observability. The handler calls
	// `Sidecars.Validate(limits)` before persisting — a failed
	// validation 400s the whole request (the sidecar is never
	// silently dropped; the customer who set it expects it to
	// apply).
	Sidecars Sidecars `json:"sidecars,omitempty"`
	// Workflows carries the ADR-081 declarative definitions alongside
	// the deployment; apid validates and persists the definitions in
	// the deployment snapshot used by workflow runs.
	Workflows []WorkflowSpec `json:"workflows,omitempty"`
	// TrafficPercent (issue #556 / traffic splitting across
	// deployments, PR-A) is the per-deployment traffic share in
	// the [0, 100] range. Pointer so that omitted == "server
	// default 100" (today's behaviour preserved exactly: 100%
	// of traffic goes to the most-recent live row). Explicit
	// values <100 enable canary: PATCH /v1/deployments/{id}/traffic
	// rebalances Σ(traffic_percent WHERE status='live') = 100
	// across siblings atomically. nil on the wire → handler
	// defaults to 100; explicit 0 → "this row receives no
	// traffic" (the rollback path); explicit 10/25/50 →
	// canary share. Plan-gated at Pro+ via
	// acct.Plan.TrafficSplitAllowed().
	TrafficPercent *int `json:"traffic_percent,omitempty"`
	// Scope (ADR-091 / PR-D) declares which named env scope this
	// deployment reads at wake time. Empty / omitted → handler
	// defaults to api.DefaultEnvScope. Migration 00213's CHECK
	// constraint enforces EnvScopePattern; the handler runs
	// api.ValidateScope before storing. A duplicate live row on
	// (app_id, scope) → 400 deployment_scope_collision.
	Scope string `json:"scope,omitempty"`
	// Annotation fields (issue #977 / ADR-116). All four are
	// optional; nil/empty on the wire = no annotation. The CLI
	// surfaces --reason / --tag / --deployed-by; the githubd
	// bridge stamps DeployedBy from pusher.name and PRNumber from
	// pull_request.number; the GitHub Action defaults DeployedBy
	// to ${{ github.actor }} and PRNumber to
	// ${{ github.event.pull_request.number }} when present.
	//
	// Reason     free-form prose, ≤280 chars (DB CHECK).
	// Tag        closed-set enum (DB CHECK; handler validates too).
	// DeployedBy human-readable actor label.
	// PRNumber   positive int (DB CHECK; 0 collapses to NULL).
	Reason     *string `json:"reason,omitempty"`
	Tag        *string `json:"tag,omitempty"`
	DeployedBy *string `json:"deployed_by,omitempty"`
	PRNumber   *int    `json:"pr_number,omitempty"`
	// Canary (issue #976 / ADR-122 / SAFE-RELEASES-A). Pointer
	// so omitted == "no canary; server-default 'none' preset"
	// (today's behaviour preserved exactly: 100% on the new
	// row). On a non-nil Canary the handler resolves Preset
	// against pkg/api/canary.LookupPreset, stamps
	// canary_preset/canary_step/canary_total_steps, and the
	// canary_progression meterd tick walks the ladder from there.
	// Plan-gated at Pro+ via acct.Plan.TrafficSplitAllowed()
	// (mirrors the TrafficPercent gate at line 922-923). nil on
	// the wire → 'none' with zero ladder.
	Canary *CanaryPresetSpec `json:"canary,omitempty"`
	// FullRootfsAllowAuto controls fallback to a self-contained OCI rootfs
	// when the image does not descend from a Gregale runtime base. Nil uses
	// the plan default; an explicit value is persisted per deployment.
	FullRootfsAllowAuto *bool `json:"full_rootfs_allow_auto,omitempty"`
	// FullRootfsOverride is the tri-state override: nil honors the plan and
	// allow-auto setting, true forces full-rootfs, and false forces the
	// legacy shared-base path.
	FullRootfsOverride *bool `json:"full_rootfs_override,omitempty"`
}

// CanaryPresetSpec is the canary ladder a customer asks for on a
// deploy (issue #976 / ADR-122 / SAFE-RELEASES-A + production-
// leveling Stream F). Preset is the catalog name from
// pkg/api/canary (none/slow/balanced/aggressive/1-10-50-100/custom).
// When Preset is "custom", Stages is the customer-supplied ladder
// (`percent` + duration string in time.ParseDuration form,
// e.g. "1% at 30s, 10% at 2m, 100% at 0s").
//
// The wire-format change (StepDurations removed, Stages added) is
// additive on the consumer side because the prior StepDurations
// field was declared-but-dead (no client construction site in
// the repo) — pre-PR clients never sent it. The CLI default
// (`--canary-preset balanced`) keeps producing a wire shape that
// matches the pre-PR form (Preset alone, no Stages).
type CanaryPresetSpec struct {
	Preset string               `json:"preset"`
	Stages []canary.CustomStage `json:"stages,omitempty"`
}

// CreateDeploymentOverrides is the optional override object on
// CreateDeploymentRequest (issue #460 / ADR-053). Six fields, frozen
// by ADR-053 §Decision 1. The handler calls Validate(limits) before
// persisting — a failed validation 400s the whole request (the
// override is never silently dropped; the customer who set it
// expects it to apply).
//
// Env / env_secrets share Limits.EnvVarsMax (ADR-045 §Decision 1 +
// ADR-053 §Decision 1): the total len(env) + len(env_secrets) is
// checked against the cap, so a customer cannot bypass the per-app
// quota by mixing the two surfaces.
//
// Env values are persisted plaintext into override_env jsonb. Env
// secret values are NOT plaintext — they are refs of the shape
// "secret:NAME" where NAME matches ^[A-Z][A-Z0-9_]*$ and resolves at
// wake time against the existing app_secrets table. The refs are
// stored verbatim in override_env_secrets jsonb; runtime resolution
// is a follow-up PR (imaged layer injection).
type CreateDeploymentOverrides struct {
	// Entrypoint replaces the OCI image's ENTRYPOINT/CMD argv when
	// the guest execs the workload. Required to be non-empty if
	// present; each element must be non-empty. nil = no override.
	Entrypoint []string `json:"entrypoint,omitempty"`
	// Cmd is appended to Entrypoint (mirrors the OCI runtime
	// contract: argv = entrypoint + cmd). nil = no override.
	Cmd []string `json:"cmd,omitempty"`
	// Env is the plaintext env map applied at boot. Key per
	// ValidateEnvKey (^[A-Z][A-Z0-9_]*$); per-value byte cap per
	// limits.EnvValueMaxBytes. nil/empty = no override.
	Env map[string]string `json:"env,omitempty"`
	// EnvSecrets is the sealed-secret-ref env map applied at boot.
	// Each VALUE is a "secret:NAME" ref (NAME matching the same
	// identifier grammar); each KEY is the env-var name set inside
	// the guest. Counts toward the same env_vars_max cap as Env.
	// nil/empty = no override.
	EnvSecrets map[string]string `json:"env_secrets,omitempty"`
	// Port is the listen port; 1..65535. 0 means "absent / fall
	// back to image default" (DefaultAppPort, today = 8080). The
	// host-side plumbing that propagates this value to netns +
	// vmmd waitReady + runners ships in PR-C; PR-A persists the
	// column and surfaces it on the response.
	Port int `json:"port,omitempty"`
	// Healthcheck is the optional readiness probe. PR-A persists
	// the shape; PR-B stamps AppManifest.Healthz at deploy time;
	// PR-D activates the runtime half — pkg/fcvm/vmm.go::waitReady
	// issues an HTTP GET against <HostIP>:8080<Healthcheck.Path>
	// and accepts 2xx (issue #460 / ADR-053 / ADR-057). Empty path
	// preserves the legacy TCP-accept behaviour. IntervalS /
	// TimeoutS / Retries are stored + validated here but remain
	// dormant until a v2 contract lands them on the wire.
	Healthcheck *DeploymentHealthcheck `json:"healthcheck,omitempty"`
	// LivenessProbe is the optional liveness probe override
	// (issue #554 / ADR-078). Per-deployment override wins over
	// the parent app's per-plan defaults (Hobby/Pro/Scale → 5s /
	// 3 consecutive / 60s cooldown / 3 in 300s). Free is gated
	// off entirely (Plan.LivenessAllowed() returns false; the
	// apid handler rejects the create with
	// CodePlanLivenessProbeNotAllowed before the DB is touched).
	// Cooldown / MaxRestarts / WindowSeconds are polymorphic per
	// the per-plan defaults but v1 does NOT expose them as
	// per-deployment knobs — only interval / timeout / consecutive.
	// Three strikes in 300s drives the park path
	// (Engine.ParkDeployment) regardless of per-deployment probe
	// tuning.
	LivenessProbe *DeploymentLivenessProbe `json:"liveness_probe,omitempty"`
	// Scope (ADR-091 / PR-D) declares which named env scope this
	// deployment reads at wake time. Empty / omitted defaults to
	// api.DefaultEnvScope at the handler. Migration 00213's
	// partial unique index `deployments_app_scope_live_uniq`
	// prevents two live deployments from sharing (app_id, scope)
	// — a duplicate returns 400 deployment_scope_collision. A
	// scope change requires a NEW deployment; UpdateDeployment
	// intentionally rejects field updates.
	Scope string `json:"scope,omitempty"`
}

// DeploymentHealthcheck is the readiness-probe shape on the
// override object. Defaults: interval 5s, timeout 2s, retries 3.
// Path is required (and must start with "/") when the parent
// healthcheck is set.
//
// M-1 (ADR-136) extended the surface additively with OCI HEALTHCHECK
// fields so a registry image's HEALTHCHECK CMD semantics flow through
// to AppManifest.Healthcheck (workstream A.4 / issue #1186). The
// `Test` argv is the canonical OCI shape — when set, runtime polling
// in M-2 will prefer Test over Path; until then Path is what
// guest-init probes (backward-compat preserved).
type DeploymentHealthcheck struct {
	Path         string   `json:"path"`
	IntervalS    int      `json:"interval_s,omitempty"`
	TimeoutS     int      `json:"timeout_s,omitempty"`
	Retries      int      `json:"retries,omitempty"`
	Test         []string `json:"test,omitempty"`
	StartPeriodS int      `json:"start_period_s,omitempty"`
}

// DeploymentLivenessProbe is the liveness-probe shape on the
// override object (issue #554 / ADR-078). The probe is the
// Cloud-Run-parity primitive that asks "is the VM still responding?"
// after N consecutive failures the host (cmd/vmmd) destroys the VM
// and schedd cold-boots it from rootfs (per ADR-005 — never
// snapshot-restore). Defaults: path is required (must start with "/");
// the period / timeout / consecutive fields are 0 = inherit from
// the parent app's per-plan defaults (Hobby/Pro/Scale → 5s / 3 / 60s).
// The window + max_restarts knobs are the park-on-exhaustion gate
// (engine.ParkDeployment on the 3rd restart in 300s).
//
// Note: the readiness probe (`DeploymentHealthcheck` above) and the
// liveness probe are intentionally separate surfaces. Readiness is
// "accept traffic?" — the app opens the readiness port AFTER its
// migration is complete. Liveness is "still alive?" — a failed
// liveness probe means the VM is wedged (busy-loop, leaked fd,
// deadlocked runner) and must be destroyed and cold-booted. A
// passing readiness but failing liveness is the canonical "wake succeeded,
// then the runtime died" failure mode that the customer-facing
// primitive on this shape is designed to catch.
type DeploymentLivenessProbe struct {
	// Path is the HTTP path the guest-init hits on the runner's
	// :8080 (issue #554 §4: reuses the existing `:8080/healthz`
	// surface, no runner changes). Required (must start with "/").
	Path string `json:"path"`
	// IntervalS is the per-plan poll cadence. 0 = inherit from
	// the parent app's per-plan default (Hobby/Pro/Scale → 5s).
	// Clamped to [MinLivenessPeriodSeconds=1, MaxLivenessPeriodSeconds=60]
	// by Validate. V1 is HTTP-only; GRPCLivenessAllowed() returns
	// false across all plans (follow-up when v2 lands).
	IntervalS int `json:"interval_s,omitempty"`
	// TimeoutS is the per-probe HTTP timeout. 0 = inherit from
	// the runner-default 2s (VsockLivenessTimeoutMs). Clamped to
	// [1, 5]. A timeout is treated identically to a non-2xx
	// response by the failure counter.
	TimeoutS int `json:"timeout_s,omitempty"`
	// ConsecutiveFailures is the N at which DestroyForLivenessFailure
	// fires. 0 = inherit from the per-plan default (3). Clamped to
	// [1, 10]. The counter is reset to 0 on the first 2xx and
	// survives an intermittent 5xx across the consecutive window
	// (AC #2 — flaky app does NOT oscillate).
	ConsecutiveFailures int `json:"consecutive_failures,omitempty"`
	// CooldownS (issue #554 / ADR-078) is the per-deployment
	// override of the vmmd-side cooldown gate. After a successful
	// DestroyForLivenessFailure, the next liveness-failure fire on
	// a fresh instance is skipped if it's within CooldownS seconds
	// of the previous destroy — gives the cold-boot instance a
	// grace window to come up without being torn down again by
	// noise. 0 = inherit from the per-plan default
	// (api.Limits.LivenessCooldownSeconds = 60s for Hobby/Pro/Scale).
	// Clamped to [MinLivenessCooldownSeconds=10,
	// MaxLivenessCooldownSeconds=600] by the apid validator.
	// Distinct from the schedd-side LivenessWindow which is the
	// "N restarts in W seconds → park deployment" gate (issue #554
	// AC #3, pkg/sched/liveness_window.go).
	CooldownS int `json:"cooldown_s,omitempty"`
}

// SecretRefPrefix is the wire prefix on env_secrets values that flags the
// value as a sealed-secret ref rather than a plaintext fallback.
// ADR-053 §Decision 1 — pkg/sched/engine.go's loadSealedEnvFor strips this
// prefix and looks up the trailing name against app_secrets at wake time.
// PR-A only validated the shape at apid time; the runtime resolver is PR-B.
const SecretRefPrefix = "secret:"

// SecretRefNameRe matches the NAME portion of a sealed-secret ref. Same
// identifier grammar as env keys / secret keys (ADR-045 §Decision 1 mirror).
// Exported so pkg/sched/engine.go::loadSealedEnvFor (PR-B) reuses it at
// wake time — one regex, no drift between apid and schedd rejection logic.
var SecretRefNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Validate enforces every override field's constraint from
// ADR-053 §Decision 1. Returns nil on success or a *Problem with
// RFC 7807 status 400 (or 413 for value-too-large, mirroring
// ErrEnvVarValueTooLarge). The handler maps this directly to
// api.WriteProblem; no further error wrapping needed.
//
// Limits is passed in by the caller (apid looks up via
// api.MustLimitsFor(acct.Plan)) so this stays a pure function —
// testable without an account / DB.
func (o *CreateDeploymentOverrides) Validate(limits Limits) *Problem {
	if o == nil {
		return nil
	}

	// entrypoint: non-empty if present; every element non-empty.
	for i, e := range o.Entrypoint {
		if e == "" {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("entrypoint[%d] is empty; every argv element must be non-empty.", i))
		}
	}

	// cmd: non-empty if present; every element non-empty.
	for i, c := range o.Cmd {
		if c == "" {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("cmd[%d] is empty; every argv element must be non-empty.", i))
		}
	}

	// env + env_secrets share EnvVarsMax. Compute total first so
	// the cap error names BOTH surfaces when both contribute.
	totalEnv := len(o.Env) + len(o.EnvSecrets)
	if limits.EnvVarsMax > 0 && totalEnv > limits.EnvVarsMax {
		return NewProblem(http.StatusBadRequest, CodeValidation,
			"Override env count exceeded",
			fmt.Sprintf("%s plan allows %d env+env_secrets entries per override; got %d (env=%d, env_secrets=%d).",
				limits.Plan, limits.EnvVarsMax, totalEnv, len(o.Env), len(o.EnvSecrets))).
			WithLimit(int64(limits.EnvVarsMax), int64(totalEnv)).
			WithDocs(docsBase + "/deploy-overrides#env")
	}

	// env: key grammar + per-value byte cap. The same byte cap
	// covers env_secrets ref strings (they are also text the
	// customer sends); the ref grammar check is below.
	for k, v := range o.Env {
		if p := ValidateEnvKey(k); p != nil {
			return p
		}
		if limits.EnvValueMaxBytes > 0 && len(v) > limits.EnvValueMaxBytes {
			return ErrEnvVarValueTooLarge(limits, len(v))
		}
	}

	// env_secrets: key grammar + "secret:NAME" ref shape + per-value
	// byte cap on the ref string.
	for k, v := range o.EnvSecrets {
		if p := ValidateEnvKey(k); p != nil {
			return p
		}
		if !strings.HasPrefix(v, SecretRefPrefix) {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("env_secrets[%q] value must start with %q (e.g. %qDB_URL); got %q.",
					k, SecretRefPrefix, SecretRefPrefix, v))
		}
		name := strings.TrimPrefix(v, SecretRefPrefix)
		if !SecretRefNameRe.MatchString(name) {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("env_secrets[%q] ref name %q must match %s.",
					k, name, SecretRefNameRe.String()))
		}
		if limits.EnvValueMaxBytes > 0 && len(v) > limits.EnvValueMaxBytes {
			return ErrEnvVarValueTooLarge(limits, len(v))
		}
	}

	// port: 0 means absent (fall back to image default). 1..65535
	// when present.
	if o.Port != 0 && (o.Port < 1 || o.Port > 65535) {
		return NewProblem(http.StatusBadRequest, CodeValidation,
			"Invalid override",
			fmt.Sprintf("port %d out of range; must be 0 (absent) or 1..65535.", o.Port))
	}

	// healthcheck: path must start with "/" if set; defaults
	// applied on Persist side (the column shape is the raw shape).
	if o.Healthcheck != nil {
		if !strings.HasPrefix(o.Healthcheck.Path, "/") {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("healthcheck.path must start with %q; got %q.",
					"/", o.Healthcheck.Path))
		}
		if o.Healthcheck.IntervalS < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("healthcheck.interval_s must be >= 0; got %d.", o.Healthcheck.IntervalS))
		}
		if o.Healthcheck.TimeoutS < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("healthcheck.timeout_s must be >= 0; got %d.", o.Healthcheck.TimeoutS))
		}
		if o.Healthcheck.Retries < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("healthcheck.retries must be >= 0; got %d.", o.Healthcheck.Retries))
		}
	}

	// liveness_probe (issue #554 / ADR-078): path must start with "/";
	// interval_s ∈ [MinLivenessPeriodSeconds, MaxLivenessPeriodSeconds]
	// when explicit; timeout_s ∈ [1, 5]; consecutive_failures ∈ [1, 10].
	// 0 = inherit from the per-plan default (the per-plan accessor
	// LivenessPeriodSeconds() / etc. handle the inheritance). The
	// handler gate (Plan.LivenessAllowed() → false on Free) is the
	// upstream check; this Validate only enforces the per-field
	// shape, NOT the per-plan gate.
	if o.LivenessProbe != nil {
		if !strings.HasPrefix(o.LivenessProbe.Path, "/") {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.path must start with %q; got %q.",
					"/", o.LivenessProbe.Path))
		}
		// IntervalS = 0 means "inherit per-plan default" — only
		// reject values that are explicitly out of range. The
		// downstream Dormant → Active transition (PR-B's runtime
		// half) reads the per-plan default via Plan.LivenessPeriodSeconds()
		// when this is 0.
		if o.LivenessProbe.IntervalS < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.interval_s must be >= 0 (0 = inherit); got %d.", o.LivenessProbe.IntervalS))
		}
		if o.LivenessProbe.IntervalS > MaxLivenessPeriodSeconds {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.interval_s must be <= %d; got %d.",
					MaxLivenessPeriodSeconds, o.LivenessProbe.IntervalS))
		}
		if o.LivenessProbe.TimeoutS < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.timeout_s must be >= 0 (0 = inherit 2s); got %d.", o.LivenessProbe.TimeoutS))
		}
		// TimeoutS ceiling is 5s (the upper bound of the wire-side
		// VsockLivenessTimeoutMs area; a 5s timeout is the longest
		// "still responsive" probe that doesn't burn the period
		// budget on a single check).
		if o.LivenessProbe.TimeoutS > 5 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.timeout_s must be <= 5; got %d.", o.LivenessProbe.TimeoutS))
		}
		if o.LivenessProbe.ConsecutiveFailures < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.consecutive_failures must be >= 0 (0 = inherit); got %d.", o.LivenessProbe.ConsecutiveFailures))
		}
		if o.LivenessProbe.ConsecutiveFailures > 10 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.consecutive_failures must be <= 10; got %d.", o.LivenessProbe.ConsecutiveFailures))
		}
		// CooldownS (issue #554 closure / ADR-078, code review
		// #725 finding F2). 0 = "no cooldown gate" (Free-plan
		// legacy behaviour, gate bypasses — see
		// cmd/vmmd/liveness_recv.go::runOne). Positive values
		// must be in [MinLivenessCooldownSeconds=10,
		// MaxLivenessCooldownSeconds=600] — the lower bound
		// stops a customer from setting cooldown=1 and
		// effectively neutering liveness for the cold-boot
		// replacement window; the upper bound stops a typo
		// (cooldown=999999) from wedging the deployment
		// (gate would short-circuit every probe for ~11.5
		// days, bypassing the §14 metal acceptance contract).
		if o.LivenessProbe.CooldownS < 0 {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.cooldown_s must be >= 0 (0 = no cooldown gate); got %d.", o.LivenessProbe.CooldownS))
		}
		if o.LivenessProbe.CooldownS > 0 && o.LivenessProbe.CooldownS < MinLivenessCooldownSeconds {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.cooldown_s must be 0 (no cooldown) or in [%d, %d]; got %d.",
					MinLivenessCooldownSeconds, MaxLivenessCooldownSeconds, o.LivenessProbe.CooldownS))
		}
		if o.LivenessProbe.CooldownS > MaxLivenessCooldownSeconds {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid override",
				fmt.Sprintf("liveness_probe.cooldown_s must be <= %d; got %d.",
					MaxLivenessCooldownSeconds, o.LivenessProbe.CooldownS))
		}
	}

	return nil
}

// BuildProvenanceResponse is the public surface of build_provenance
// (ADR-038, Tier 3 / issue #197 B3.10-read half). Field names mirror
// the table columns with snake_case naming so the customer-visible
// JSON stays self-documenting on a `curl`.
//
// Fields are nullable strings; empty values map to "" so the customer
// reads "buildkit_version = \"\"" for a pre-Phase-3 build that the
// populator hasn't back-filled. The dashboard branches on
// `sbom_storage_key != ""` to enable the "Download SBOM" link;
// every other field is observational metadata for audits.
type BuildProvenanceResponse struct {
	ID             string `json:"id"`
	BuildID        string `json:"build_id"`
	BuildkitVer    string `json:"buildkit_version"`
	RailpackVer    string `json:"railpack_version"`
	BaseDigest     string `json:"base_digest"`
	SourceSHA256   string `json:"source_sha256"`
	SourceURL      string `json:"source_url"`
	CommitSHA      string `json:"commit_sha"`
	Plan           string `json:"plan"`
	RunnerDigest   string `json:"runner_digest"`
	BuilderNodeID  string `json:"builder_node_id"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	SBOMStorageKey string `json:"sbom_storage_key"`
	FrameworkVer   string `json:"framework_version"`
}

// BuildResponse is the public surface of a builds row (DEPLOY-PROV-6 /
// ADR-089, issue #741). Companion to BuildProvenanceResponse (post-
// mortem export, ADR-038) and the /sbom route (post-mortem blob,
// ADR-038 Phase 3): BuildResponse is the LIFECYCLE surface — status,
// timestamps, failure_class, server-computed duration.
//
// Status mirrors builds.status, a 4-state enum (queued|running|
// succeeded|failed) — schema.sql:662 CHECK constraint. 'cancelled'
// from the original issue example is intentionally absent; the
// schema doesn't support it and no transition code exists. Adding
// it requires a separate migration + builderd path.
//
// failure_class is empty unless status='failed'; the failure_class
// CHECK constraint is oom|timeout|user_error|infra (schema.sql:660).
// error_message is NOT in this response — the detailed per-failure
// string lives on deployments.error_message; clients that need it
// should call GetDeployment(deployment_id). ADR-089 §4.
//
// duration_seconds is server-computed (FinishedAt-StartedAt) only
// when both timestamps are set; the field is omitted otherwise so a
// queued/running build stays minimal. CI scripts shouldn't have to
// parse RFC3339 to compute elapsed time.

// Build status wire constants (ADR-091, DEPLOY-PROV-6 follow-up).
// The 4-value enum is enforced by builds_status_check in
// schema.sql:651 — clients see exactly one of these strings on
// BuildResponse.Status. Name-spaced (not the bare string) so
// goconst stops flagging the literal at 3+ hits across the
// build/deploy surface. Mirror state.BuildStatus* on the state
// side (pkg/state/types.go:123) but with the `BuildStatus` prefix
// here on the wire side, per ADR-091 §3 + the memory note
// goconst-status-literal-multi-resource.
const (
	BuildStatusQueued    = "queued"
	BuildStatusRunning   = "running"
	BuildStatusSucceeded = "succeeded"
	BuildStatusFailed    = "failed"
)

type BuildResponse struct {
	ID              string `json:"id"`
	DeploymentID    string `json:"deployment_id"`
	Kind            string `json:"kind"` // railpack|dockerfile|tarball|github
	SourceBytes     int64  `json:"source_bytes"`
	Status          string `json:"status"` // queued|running|succeeded|failed
	FailureClass    string `json:"failure_class,omitempty"`
	LogPath         string `json:"log_path,omitempty"`
	EnqueuedAt      string `json:"enqueued_at"`
	StartedAt       string `json:"started_at,omitempty"`
	FinishedAt      string `json:"finished_at,omitempty"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

// BuildListResponse is the page shape for GET /v1/builds
// (DEPLOY-PROV-6 follow-up / ADR-091, issue #741 close-out).
// Items is the page (started_at desc nulls last); NextBefore is
// the cursor for the next page (empty = end of list). The cursor
// is the started_at of the LAST row with a non-null started_at on
// this page, formatted as RFC3339Nano UTC. Mirrors
// DeploymentListResponse.
//
// nulls-last rationale: queued builds (started_at IS NULL) sort
// to the bottom of the first page; the handler walks the page
// backward to find the LAST non-null started_at for the cursor
// so passing next_before never skips the running/succeeded rows
// behind queued builds at the tail of the previous page.
type BuildListResponse struct {
	Items      []BuildResponse `json:"items"`
	NextBefore string          `json:"next_before,omitempty"`
}

// CancelDeploymentRequest is the optional body of POST
// /v1/apps/{slug}/deployments/{id}/cancel. Reason must be one of
// the closed pkg/state.CancelReason values (empty → "user" server-side).
type CancelDeploymentRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ClearObsoleteReport is the response shape for POST
// /v1/apps/{slug}/deployments/clear-obsolete. Count is the number
// of soft-deleted rows in this call; OlderThan echoes the cutoff
// the store applied (default 168h).
type ClearObsoleteReport struct {
	AppSlug   string `json:"app_slug"`
	Count     int    `json:"count"`
	OlderThan string `json:"older_than"`
}

// DeploymentAuditResponse is one row of the deployment_audit
// timeline (issue #976 / ADR-122 / SAFE-RELEASES-E.2 + production
// leveling Stream A). Mirrors pkg/state.DeploymentAudit but drops
// the internal DB id (BIGINT) — the wire surface exposes the audit
// row as a sequence-pointed event keyed by (deployment_id, at).
// Data is the verbatim jsonb payload at emit time (kind-specific
// shape — DeployTrafficChanged carries {from_percent, to_percent,
// actor_kind}, DeployRolledBack carries {target_deployment_id,
// reason}).
type DeploymentAuditResponse struct {
	At        string          `json:"at"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor"`
	Data      json.RawMessage `json:"data,omitempty"`
	AccountID string          `json:"account_id,omitempty"`
	// AlertRuleID (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122)
	// is the alert_rule.id that triggered this audit row when
	// non-nil. Closed set: nil for orchestrator-lifecycle rows
	// (deploy.rollout_*, deploy.canary_step_advanced); non-nil
	// for the deploy.alert_rule_fired + rollback/demote/promote
	// paths. The /dashboard/alerts/{id} handler uses this to
	// reverse-link from the audit timeline back to the firing
	// rule. Wire-additive per ADR-016 (pre-PR consumers see no
	// field; post-PR consumers see "alert_rule_id": "<uuid>").
	AlertRuleID *uuid.UUID `json:"alert_rule_id,omitempty"`
}

// ListDeploymentAuditResponse is the paginated wrapper for
// `GET /v1/deployments/{id}/audit`. The dashboard uses this to
// render the per-deployment timeline; programmatic consumers
// (SDK + `gregale deployments audit <id>`) use the same shape.
//
// limit is echoed back so a paging consumer can distinguish
// "limit was clamped" from "no more rows" — both yield Items of
// length < limit, but the clamping is observable via this field.
type ListDeploymentAuditResponse struct {
	Items []DeploymentAuditResponse `json:"items"`
	Limit int                       `json:"limit"`
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
	// fields so post-mortem retrieval via `gregale deployment <id>`
	// or `gregale inspect <slug> --errors` surfaces the same
	// 3-5 line shape that the deploy-time Problem emits. Empty for
	// deployments created before migrations/00290 OR that are not
	// in a failure state — the dashboard branches on the same
	// ErrorCode != "" test and renders the four together.
	ErrorHint string `json:"error_hint,omitempty"`
	ErrorWhy  string `json:"error_why,omitempty"`
	ErrorFix  string `json:"error_fix,omitempty"`
	// ErrorRelevantLogs is the last N log lines that explain the
	// failure, surfaced inline by the dashboard when the deployment
	// row carries them. Capped at 20 entries × 512 bytes each (CLI
	// tripwire; see pkg/whycopy.Render for the catalogue row).
	ErrorRelevantLogs []LogExcerpt `json:"error_relevant_logs,omitempty"`
	CreatedAt         string       `json:"created_at"`
	// SourceRoot is the repository-relative build root used by a workspace
	// context upload. Empty means the archive root and is omitted for legacy
	// self-contained source deploys.
	SourceRoot string `json:"source_root,omitempty"`
	// HasOverrides is true when the deployment carries an
	// override_* column set (issue #460 / ADR-053). Lets dashboards
	// render "this deploy pinned overrides" without re-parsing the
	// six sibling fields.
	HasOverrides bool `json:"has_overrides,omitempty"`
	// OverrideEntrypoint is the argv override echoed verbatim; nil
	// when the deployment carried no override. ADR-053 §Decision 4:
	// these fields are non-secret and safe to echo.
	OverrideEntrypoint []string `json:"override_entrypoint,omitempty"`
	// OverrideCmd is the cmd override echoed verbatim; nil when
	// the deployment carried no override.
	OverrideCmd []string `json:"override_cmd,omitempty"`
	// OverrideEnvKeys is the set of env-var keys set by the env
	// override. VALUES ARE NEVER ECHOED (ADR-053 §Decision 4 +
	// ADR-045 §Decision 6 mirror). Empty when no env override.
	OverrideEnvKeys []string `json:"override_env_keys,omitempty"`
	// OverrideEnvSecretKeys is the set of env-var keys set by the
	// env_secrets override. VALUES (the "secret:NAME" refs) are
	// echoed verbatim because the ref shape is non-secret by
	// design — the customer needs to see which secret they bound
	// to which env var to debug a misconfigured deploy. Empty
	// when no env_secrets override.
	OverrideEnvSecretKeys []string `json:"override_env_secret_keys,omitempty"`
	// OverrideEnvSecretRefs is the verbatim "secret:NAME" map,
	// parallel to OverrideEnvSecretKeys. Same rationale: the refs
	// are non-secret. nil when no env_secrets override.
	OverrideEnvSecretRefs map[string]string `json:"override_env_secret_refs,omitempty"`
	// OverridePort is the listen-port override (0 = absent /
	// fall back to image default). ADR-053 §Decision 1.
	OverridePort int `json:"override_port,omitempty"`
	// OverrideHealthcheck is the readiness-probe override
	// verbatim. Persisted; the actual HTTP probe is a follow-up.
	OverrideHealthcheck *DeploymentHealthcheck `json:"override_healthcheck,omitempty"`
	// OverrideLivenessProbe is the liveness-probe override
	// verbatim (issue #554 / ADR-078). nil when the deployment
	// used the per-plan default (Hobby/Pro/Scale → 5s / 3
	// consecutive / 60s cooldown). Echoed on GET
	// /v1/apps/{slug}/deployments/{id} so the customer can audit
	// which probe the host is running against the VM.
	OverrideLivenessProbe *DeploymentLivenessProbe `json:"override_liveness_probe,omitempty"`
	// MinInstances is the per-deployment cold-wake floor override
	// (issue #557 closure / ADR-072). 0 = "inherit from parent
	// app" (the post-migration default); a positive value is the
	// deployment's own floor. Effective per-instance floor =
	// max(app.EffectiveMinInstances(), d.EffectiveMinInstances()).
	MinInstances int `json:"min_instances"`
	// Scan is the per-deploy grype CVE scan surface (issue #464
	// / ADR-055, PR-1). nil for pre-feature rows (the migration
	// backfilled scan_status='skipped' + scan_result={reason:
	// 'pre-feature'} on those, but the apid read path returns
	// nil so the dashboard / CLI see a clean absence — the
	// /scan route surfaces the 'skipped' sentinel for those
	// rows). Always non-nil for post-feature rows in any of
	// the {pending, complete, failed, skipped} states.
	Scan *ScanResult `json:"scan,omitempty"`
	// SecretScan is the per-deploy secret-scan audit row (PR-A,
	// imaged-layer; distinct from the cmd/apid source-tree path).
	// Mirrors the Scan field shape: nil when the row has not
	// been scanned yet, non-nil with Findings=[] for a clean
	// scan, non-nil with Findings=[…] for a hit. Read by the
	// dashboard's "secret scan" card and the CLI's
	// `--show-secret-scan` flag. The `omitempty` matches Scan so
	// absence == "scan pending" on both surfaces — the
	// /v1/deployments/{id}/secret-scan route is the
	// 404-on-missing drilldown.
	SecretScan *SecretScanResult `json:"secret_scan,omitempty"`
	// ParkedReason / ParkedAt (issue #554 / ADR-079 follow-up)
	// surface the per-deployment parking columns from migration
	// 00157 on the GET /v1/deployments/{id} response. omitempty
	// mirrors LastScaleOutAt — "never parked" → no field on the
	// wire. The closed-set vocabulary is enforced at the schema
	// layer via the deployments_parked_reason_check constraint.
	ParkedReason string     `json:"parked_reason,omitempty"`
	ParkedAt     *time.Time `json:"parked_at,omitempty"`
	// TrafficPercent (issue #556 / traffic splitting across
	// deployments, PR-A) is the per-deployment traffic share
	// surfaced on GET /v1/deployments/{id} and GET
	// /v1/apps/{slug} (the per-deployment live row only — the
	// App-level aggregate traffic split is PR-B/C scope). Always
	// present in [0, 100]; the migration stamps 100 on every
	// pre-feature row, the handler defaults to 100 on create, and
	// the supersede step inside CreateDeployment's transaction
	// stamps 0 on the prior row so Σ=100 trivially for the
	// one-live-deployment case. See migration 00160 and
	// pkg/state.UpdateDeploymentTraffic for the rebalance
	// semantics.
	TrafficPercent int `json:"traffic_percent"`
	// Scope (ADR-091 / PR-D) echoes the deployment's
	// env-targeting scope. Surfaces on GET /v1/deployments/{id}
	// and the per-app live list (pkg/state.SerializeDeployment
	// reads dep.Scope — already populated by the SELECT
	// projection pgstore loads). omitempty keeps the wire clean
	// for the (rare) pre-PR-D row where scope defaulted to
	// "default" but the column itself was backfilled; the
	// SerializeDeployment projector always writes the explicit
	// value, so downstream consumers see "default" on pre-PR
	// fixtures the moment PR-D ships. Migration 00213's CHECK
	// ensures the value is a valid slug; the handler validates
	// scopeFromBody before storing via api.ValidateScope.
	Scope string `json:"scope,omitempty"`
	// BuildPlan (issue #961 / Mega-A PR-2) carries the
	// framework + runtime + version + entrypoint + port + class
	// that the build pipeline detected or that the deployment
	// was created with. nil when the deployment is an image
	// deploy (no source tarball to detect from) — omitempty
	// keeps the wire byte-identical for pre-PR-2 clients.
	// String fields only because pkg/api cannot import pkg/state
	// (the App.Type enum lives in pkg/state/types.go).
	BuildPlan *BuildPlan `json:"build_plan,omitempty"`
	// Issue #606 / SAFE-RELEASES-E.1: structured deployer
	// attribution. All four fields are server-stamped from the
	// HTTP request context (never client-supplied) and use
	// `omitempty` so pre-#606 rows render unchanged on the wire
	// (empty strings drop from the JSON). The closed-set
	// vocabulary for DeployedVia is enforced at the schema
	// layer via migrations/00305's CHECK constraint; the FK on
	// DeployedByUserId is ON DELETE SET NULL so revoking an
	// account never cascades into a deleted deployment row.
	//
	// DeployedByUserId is the deploying account's UUID (FK →
	// accounts.id, ON DELETE SET NULL). Empty when the deploy
	// came from a non-local source we couldn't resolve (e.g. a
	// githubd pusher whose email isn't bound to a local
	// account — PusherLogin carries the raw GH login in that
	// case so the deployment row is still attributable on
	// read-back).
	DeployedByUserID string `json:"deployed_by_user_id,omitempty"`
	// DeployedVia is the closed-set classifier of how this
	// deployment was submitted. One of "api" / "cli" /
	// "dashboard" / "github" / "operator". Computed at handler
	// entry by inspecting session cookie vs bearer token vs API
	// key vs the githubd_bridge call shape; the schema CHECK
	// constraint (deployments_deployed_via_set_chk) rejects
	// any value outside this set.
	DeployedVia string `json:"deployed_via,omitempty"`
	// DeployedFromIp is the trusted remote IP captured by
	// pkg/middleware.ClientIP at handler entry. Uses the same
	// XFF + loopback trust contract as the auth-limit bucket;
	// diverging from that would silently make a credential-
	// stuffing burst look like a different (smaller) attack.
	// Rendered as a string (the Go INET scan path coalesces
	// empty → "" so pre-#606 rows surface unchanged).
	DeployedFromIP string `json:"deployed_from_ip,omitempty"`
	// PusherLogin is the raw GitHub login of the pusher when
	// DeployedVia == "github". Empty for all other via values
	// (the handler stamps it from the githubd_bridge req.Pusher
	// proto field). Distinct from the human-readable DeployedBy
	// text column that PR #984 / issue #977 adds — PusherLogin
	// is the unmodified GH identity, suitable for downstream
	// GitHub-API correlation.
	PusherLogin string `json:"pusher_login,omitempty"`
	// Annotation echo (issue #977 / ADR-116). Mirrors the four
	// columns from migration 00346 verbatim onto the wire so the
	// dashboard, CLI history, and SDK consumers can render the
	// annotation without an audit round-trip. omitempty on each
	// so pre-feature rows return the old wire shape with all
	// four absent. The closed-set vocabulary on `tag` and the
	// length cap on `reason` are enforced at the schema layer
	// (deployments_tag_set_chk / deployments_reason_len_chk).
	Reason     string `json:"reason,omitempty"`
	Tag        string `json:"tag,omitempty"`
	DeployedBy string `json:"deployed_by,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
	// Issue #961 leaf 8 / ADR-118 / Mega-C PR-2: per-deployment
	// auto-rollback echo. rollback_on_5xx is always present on
	// the wire (false for pre-PR-2 rows; the column has a NOT
	// NULL DEFAULT false so pgx scans it cleanly into the bool).
	// first_wake_at / first_5xx_window_ends_at / last_auto_rollback_at
	// are nullable timestamps, stamped by schedd when the gateway
	// emits the corresponding wake kind; omitempty keeps pre-PR-2
	// rows byte-identical to the old wire shape. first_5xx_count
	// is a non-nullable counter (default 0 in the schema). The
	// closed-set vocabulary on last_auto_rollback_reason is enforced
	// at the schema layer via deployments_last_auto_rollback_reason_check;
	// the wire projection coalesces NULL → '' so pre-rollback rows
	// omit the field.
	RollbackOn5xx          bool       `json:"rollback_on_5xx"`
	FirstWakeAt            *time.Time `json:"first_wake_at,omitempty"`
	First5xxWindowEndsAt   *time.Time `json:"first_5xx_window_ends_at,omitempty"`
	First5xxCount          int        `json:"first_5xx_count"`
	LastAutoRollbackAt     *time.Time `json:"last_auto_rollback_at,omitempty"`
	LastAutoRollbackReason string     `json:"last_auto_rollback_reason,omitempty"`
	// Canary ladder echo (issue #976 / ADR-122 /
	// SAFE-RELEASES-A). CanaryPreset is the catalog name; the
	// handler resolves it via pkg/api/canary.LookupPreset so
	// "1-10-50-100" surfaces as the alias of "balanced" on the
	// wire. CanaryStep / CanaryTotalSteps are the in-progress
	// ladder position (advanced by the canary_progression
	// meterd tick on a wall-clock boundary). CanaryStepStartedAt
	// is the wall-clock anchor for the current step's Duration
	// gate. omitempty keeps pre-PR rows byte-identical to the
	// old wire shape (every column defaulted to 'none'/0/NULL
	// at the schema layer via PG11+ fast-default).
	CanaryPreset        string     `json:"canary_preset,omitempty"`
	CanaryStep          int        `json:"canary_step,omitempty"`
	CanaryTotalSteps    int        `json:"canary_total_steps,omitempty"`
	CanaryStepStartedAt *time.Time `json:"canary_step_started_at,omitempty"`
	// Rollout state machine echo (issue #976 / ADR-122 /
	// SAFE-RELEASES-F). One of pending/rolling_out/complete/
	// aborted (closed set at deployments_rollout_state_chk).
	// Transitions are owned by pkg/safedeploy.Orchestrator.Once
	// (commit 5) and by the manual gregale rollouts recover
	// CLI (commit 6). omitempty so pre-PR rows render the
	// old wire shape (rollout_state='pending' is the
	// fast-default zero-value but omitempty drops it because
	// 'pending' isn't "" — the dashboard fills in from
	// pkg/state.SerializeDeployment which always stamps the
	// resolved value).
	RolloutState         string     `json:"rollout_state,omitempty"`
	RolloutStartedAt     *time.Time `json:"rollout_started_at,omitempty"`
	RolloutCompletedAt   *time.Time `json:"rollout_completed_at,omitempty"`
	RolloutAbortedAt     *time.Time `json:"rollout_aborted_at,omitempty"`
	RolloutAbortedReason string     `json:"rollout_aborted_reason,omitempty"`
}

// BuildPlan describes what the build pipeline did with the source
// (issue #961 / Mega-A PR-2). Framework is required (so dashboards
// can branch on "unknown" for monorepos); the rest are optional and
// surface only when the build pipeline populated them.
type BuildPlan struct {
	Framework  string `json:"framework"` // node|python|go|docker|unknown
	Runtime    string `json:"runtime,omitempty"`
	Version    string `json:"version,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Port       int    `json:"port,omitempty"`
	Class      string `json:"class,omitempty"` // app|function
}

// UpdateDeploymentRequest is the body for PATCH /v1/deployments/{id}
// (issue #557 closure / ADR-072). MinInstances is the only mutable
// field on a deployment — the image / digest / overrides / sidecars
// are immutable post-create (a new deployment is the canonical way
// to change them).
type UpdateDeploymentRequest struct {
	MinInstances *int `json:"min_instances"`
}

// DeploymentPreviewURL is the response body for GET
// /v1/deployments/{id}/url (issue #976 / ADR-122 /
// SAFE-RELEASES-C.2). The endpoint reads the deployment row,
// resolves the (ordinal, slug) pair via the store, and returns
// the per-deployment preview hostname the cert allowlist will
// mint under — alongside an alive flag the dashboard and CLI
// flip on to decide whether to render the "copy URL" button.
//
// The shape is split from DeploymentResponse because the
// preview URL is a derived field (it depends on the live
// Allowlist's deploySuffix, which can rotate when the platform
// migrates *.apps.gregale.dev → *.gregale.dev) rather than a
// stored column on the row.
//
// Host is empty in two cases, both deliberate:
//   - alive=false (deployment is failed/superseded) — the URL
//     isn't useful, but the row exists so a 404 would mislead
//     the dashboard into thinking the deployment doesn't.
//   - DeployWildcardSuffix is empty (deployment-preview zone
//     disabled on this platform) — the c.2 wire response
//     surfaces this for staging paths that don't mint
//     deployment-preview certs.
//
// LastCheckedAt is the timestamp the cert-issuance store last
// verified the wildcard cert on the host. nil when the host
// has never been touched by certmagic (the on-demand mint is
// lazy — a non-TLS request never validated). NOT latency — the
// cert's NotAfter is the load-bearing expiry; LastCheckedAt
// is a "has certmagic ever touched this hostname?" signal.
type DeploymentPreviewURL struct {
	// DeploymentID is the same id from the path — echoed so
	// batch callers can correlate without their own join.
	DeploymentID string `json:"deployment_id"`
	// AppID is the resolved parent app — echoed so the dashboard
	// can fetch the parent without a second round-trip.
	AppID string `json:"app_id"`
	// Host is the per-deployment preview hostname (`deploy-{N}.{slug}.gregale.dev`).
	// Empty when alive=false or the deployment-preview zone is disabled.
	Host string `json:"host,omitempty"`
	// URL is the full request URL (https + host) — empty when host is empty.
	URL string `json:"url,omitempty"`
	// Alive is the deployment-preview status — true iff the
	// deployment row exists, belongs to the caller, and has a
	// status in {pending, building, imaging, snapshotting,
	// live} (the same predicate the allowlist uses, hoisted to
	// the read-path so the dashboard doesn't have to round-trip
	// it separately).
	Alive bool `json:"alive"`
	// LastCheckedAt is when certmagic last validated the cert
	// under Host. nil for never-touched hostnames.
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

// UpdateDeploymentTrafficRequest is the body for
// PATCH /v1/deployments/{id}/traffic (issue #556 PR-A). The PATCH
// route is dedicated to traffic splitting rather than reusing
// UpdateDeploymentRequest because the request shape differs:
// TrafficPercent MUST be present (a no-field PATCH is meaningless
// here), and the pre-existing PATCH at /v1/deployments/{id} has
// its own "min_instances required" presence rule. Splitting the
// DTOs keeps each handler's contract crisp.
type UpdateDeploymentTrafficRequest struct {
	TrafficPercent int `json:"traffic_percent"`
}

// AdvanceCanaryRequest is the compare-and-swap body for
// POST /v1/deployments/{id}/canary/advance. The server derives the next
// traffic percentage from the deployment's persisted canary preset; the
// caller only supplies the step it observed, so a stale or concurrent worker
// cannot choose an arbitrary traffic value.
type AdvanceCanaryRequest struct {
	ExpectedStep int `json:"expected_step"`
}

// CanaryAdvanceResponse carries the atomically advanced deployment and the
// deployment_audit row id written in the same transaction.
type CanaryAdvanceResponse struct {
	Deployment DeploymentResponse `json:"deployment"`
	AuditID    string             `json:"audit_id"`
}

// CreateMirrorRuleRequest is the body for
// POST /v1/apps/{slug}/mirrors (issue #72 / ADR-125 traffic
// mirroring PR-A2). The (SourceDeploymentID, MirrorDeploymentID)
// pair MUST both reference live deployments belonging to the
// slug's app (the store enforces this in
// CreateMirrorRuleIfUnderQuota's FOR UPDATE lock). Percent is the
// fan-out fraction [0, 100] (0 disables the rule without
// removing it; 100 mirrors every customer request). IncludeBody
// defaults to false — sensitive bodies stay off by default per
// the spec hint; customers who want body comparison pass
// --include-body at the CLI / set IncludeBody=true here. The
// RedactHeaders list is the customer's *additive* redact set;
// the always-stripped headers (Authorization, Cookie, Set-Cookie,
// X-API-Key, Proxy-Authorization, WWW-Authenticate) are stripped
// at the gateway regardless of this field (see ADR-124 §D8).
type CreateMirrorRuleRequest struct {
	SourceDeploymentID string   `json:"source_deployment_id"`
	MirrorDeploymentID string   `json:"mirror_deployment_id"`
	Percent            int      `json:"percent"`
	IncludeBody        bool     `json:"include_body"`
	RedactHeaders      []string `json:"redact_headers"`
}

// UpdateMirrorRuleRequest is the body for
// PATCH /v1/apps/{slug}/mirrors/{id} (issue #72 / ADR-125 PR-A2).
// Every field is a pointer so the handler can distinguish "field
// absent" from "field set to zero value" (Percent=0 is legal and
// distinct from "PATCH with no body"). RedactHeaders uses a
// pointer-to-slice so a PATCH with `"redact_headers": []` clears
// the customer's additive list; a PATCH that omits the field
// leaves it untouched.
type UpdateMirrorRuleRequest struct {
	Percent       *int      `json:"percent,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
	IncludeBody   *bool     `json:"include_body,omitempty"`
	RedactHeaders *[]string `json:"redact_headers,omitempty"`
}

// MirrorRuleResponse is the canonical mirror-rule response
// (issue #72 / ADR-125 PR-A2). Returned by POST (201), GET
// (200), PATCH (200), and as a list element under
// MirrorRuleListResponse. Snake-case tags; no `omitempty` on
// response fields (clients can detect absent vs zero via the
// same call). The AlwaysStrippedHeaders field documents the
// always-stripped set so the customer can render a complete
// redaction manifest in their UI without consulting the docs.
type MirrorRuleResponse struct {
	ID                    string    `json:"id"`
	AccountID             string    `json:"account_id"`
	AppID                 string    `json:"app_id"`
	SourceDeploymentID    string    `json:"source_deployment_id"`
	MirrorDeploymentID    string    `json:"mirror_deployment_id"`
	Percent               int       `json:"percent"`
	Enabled               bool      `json:"enabled"`
	IncludeBody           bool      `json:"include_body"`
	RedactHeaders         []string  `json:"redact_headers"`
	AlwaysStrippedHeaders []string  `json:"always_stripped_headers"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// MirrorRuleListResponse wraps GET /v1/apps/{slug}/mirrors
// (issue #72 / ADR-125 PR-A2). Count is informational — the
// store's ListMirrorRules returns at most Limits.MirrorTargetsPerApp
// rows (1-3), so no pagination cursor is needed in A2. If A3's
// multi-target mirror follow-on widens the cap, this struct
// gets a NextCursor field with the standard Opaque-string
// envelope.
type MirrorRuleListResponse struct {
	Rules []MirrorRuleResponse `json:"rules"`
	Count int                  `json:"count"`
}

// MirrorSummaryResponse is the body of
// GET /v1/apps/{slug}/mirrors/{id}/summary?window={1h|24h|7d}
// (issue #72 / ADR-125 PR-A2). All counts are pre-aggregated
// server-side via SQL aggregates (COUNT / SUM / p99_cont) — the
// client never iterates the ledger. MeanLatencyDiffMs /
// P99LatencyDiffMs are *signed* (mirror_ms − source_ms; positive
// = mirror is slower). CrashCount counts the rows where the
// mirror VM exited abnormally before producing a response (the
// customer's source request still succeeded). WindowSeconds is
// the parsed window in seconds so the CLI can render "last 1h"
// without parsing the query string.
type MirrorSummaryResponse struct {
	TotalInvocations  int64 `json:"total_invocations"`
	StatusDiffCount   int64 `json:"status_diff_count"`
	SchemaDiffCount   int64 `json:"schema_diff_count"`
	BodyDiffCount     int64 `json:"body_diff_count"`
	MeanLatencyDiffMs int64 `json:"mean_latency_diff_ms"`
	P99LatencyDiffMs  int64 `json:"p99_latency_diff_ms"`
	CrashCount        int64 `json:"crash_count"`
	WindowSeconds     int   `json:"window_seconds"`
}

// MirrorWindowDuration is the parsed window argument for the
// summary endpoint (issue #72 / ADR-125 PR-A2). Three discrete
// values — the customer's "tell me drift over recent traffic"
// question doesn't need a free-form duration; these three cover
// the typical UI surfaces (live dashboard / daily review /
// weekly report).
type MirrorWindowDuration int

const (
	// MirrorWindow1h is the default; aligns with the dashboard's
	// "last hour" panel.
	MirrorWindow1h MirrorWindowDuration = 3600
	// MirrorWindow24h covers a full day; matches the customer-facing
	// "yesterday's drift" report.
	MirrorWindow24h MirrorWindowDuration = 86400
	// MirrorWindow7d covers a rolling week; matches the SLA-style
	// report the customer sends to their own stakeholders.
	MirrorWindow7d MirrorWindowDuration = 604800
)

// errInvalidMirrorWindow is the parse-failure sentinel returned
// by ParseMirrorWindow when the input is anything other than
// "" / "1h" / "24h" / "7d". Unexported because the canonical
// HTTP-shaped surface is the ErrInvalidMirrorWindow constructor
// in errors.go; the sentinel is internal to the parser so a
// future caller in this package can errors.Is on it. Cross-package
// callers should rely on the constructor's code + status, not
// this sentinel.
var errInvalidMirrorWindow = fmt.Errorf("invalid mirror window (must be 1h, 24h, or 7d)")

// ParseMirrorWindow parses a string ("1h" | "24h" | "7d") into the
// typed duration. Empty input falls through to the 1h default.
// Returns the parse-failure sentinel (errors.Is(err,
// errInvalidMirrorWindow) works inside this package; outside,
// use the ErrInvalidMirrorWindow constructor's code field) on
// any other input so the handler can return 422
// invalid_mirror_window.
func ParseMirrorWindow(s string) (MirrorWindowDuration, error) {
	switch s {
	case "", "1h":
		return MirrorWindow1h, nil
	case "24h":
		return MirrorWindow24h, nil
	case "7d":
		return MirrorWindow7d, nil
	}
	return 0, errInvalidMirrorWindow
}

// MirrorAlwaysStrippedHeaders is the set of headers the gateway
// strips from every mirror invocation regardless of the
// customer-supplied RedactHeaders list (issue #72 / ADR-125
// PR-A2 §D8). Authentication-bearing headers stay out of the
// ledger — they would either leak the customer's session into
// their own mirror, or fail JCS canonicalisation on the way in.
// This list is rendered into the MirrorRuleResponse so customers
// can render a "redacted headers" manifest in their UI.
var MirrorAlwaysStrippedHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"X-API-Key",
	"Proxy-Authorization",
	"WWW-Authenticate",
}

// RollbackRequest is the body for POST /v1/apps/{slug}/rollback.
//
// All fields are optional. With an empty body the handler falls back to
// "rollback to the most recent superseded deployment" (the pre-G
// behaviour). With TargetDeploymentID set, the handler validates that the
// deployment belongs to this app AND has status='superseded' — both are
// hard requirements; rolling back to a deployment that is already live, or
// that belongs to a different app, is rejected with a typed error rather
// than silently no-op'd.
//
// SAFE-RELEASES-G (issue #976). Forward-compatible with SAFE-RELEASES-C
// (per-deployment URL): the response carries the same deployment_id so
// -C can build a hostname off it without a wire-shape change.
type RollbackRequest struct {
	// TargetDeploymentID is the UUID of the deployment to promote back
	// to 'live'. Must belong to the same app as the URL slug, and must
	// have status='superseded' (rolling back to the already-current
	// deployment is rejected explicitly). Nil/empty falls back to the
	// most-recent superseded deployment (legacy behaviour).
	TargetDeploymentID *string `json:"target_deployment_id,omitempty"`

	// AlertRuleID (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122):
	// when set, the handler stamps the deployment_audit row's
	// alert_rule_id column with this UUID so an operator can click
	// through from the audit timeline to /dashboard/alerts/{id} and
	// see which alert rule triggered the auto-rollback. Wire-additive
	// per ADR-016; the field is ignored when nil/empty (legacy
	// operator-driven rollbacks carry no rule attribution). Only
	// privileged in-process callers (ActionDispatcher via meterd)
	// set this; the API does not enforce role because the
	// underlying endpoint already requires MFA + ScopesDeployWrite.
	AlertRuleID *string `json:"alert_rule_id,omitempty"`
}

// AccountResponse is the whoami payload. Limits is the plan's
// quota/limit table (RAM MB, max concurrency, included GB-h,
// deployed-app cap) so the dashboard /account page can show
// "you have X of Y apps" without a second round trip. UsageGBHours
// is the roll-up for the current month (caller-aggregated from
// Store.UsageByHour in apid; included here so the dashboard can
// render the meter in one fetch).
type AccountResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	// EmailVerificationGraceEndsAt is present only for unverified password
	// accounts so API and dashboard clients can render the 30-day deadline.
	EmailVerificationGraceEndsAt *time.Time    `json:"email_verification_grace_ends_at,omitempty"`
	Plan                         string        `json:"plan"`
	Status                       string        `json:"status"`
	Limits                       AccountLimits `json:"limits"`
	UsageGBHours                 float64       `json:"usage_gb_hours"`
	AppCount                     int           `json:"app_count"`
	GitHubInstall                string        `json:"github_install_id,omitempty"`
	// PlanChangeStatus and RequestedPlan are populated only when a plan
	// change was accepted by the billing provider but is not yet reflected
	// in the local entitlement. Keeping the current Plan in this response
	// makes the confirmation boundary explicit to API and CLI callers.
	PlanChangeStatus string     `json:"plan_change_status,omitempty"`
	RequestedPlan    string     `json:"requested_plan,omitempty"`
	EffectiveAt      *time.Time `json:"effective_at,omitempty"`
}

// AccountLimits is the read-only copy of api.Limits that survives
// serialization. Stripped of fields the dashboard doesn't need
// (eg. internal ops); mirror pkg/api/limits.go for the wiring.
type AccountLimits struct {
	Plan            string `json:"plan"`
	RAMMB           int    `json:"ram_mb"`
	MaxConcurrency  int    `json:"max_concurrency"`
	DeployedApps    int    `json:"deployed_apps"`
	IncludedGBHours int64  `json:"included_gb_hours"`
	AppLayerMaxMB   int    `json:"app_layer_max_mb"`
}

// APIKeyResponse is an API key returned to the customer. The plaintext
// appears ONLY on creation (POST /v1/keys, POST /v1/orgs/{slug}/keys)
// and rotation (POST /v1/keys/{id}/rotate,
// POST /v1/orgs/{slug}/keys/{id}/rotate), never on GET — only the
// prefix + label + scopes + last_used_at + id + status are returned
// thereafter. Scopes is the explicit permission set attached to the key
// (e.g. ["admin"], ["apps:read", "deploy:write"]); see ADR-034 rev2.
//
// IAM-5 (issue #189): ExpiresAt is RFC3339; omitted when the key never
// expires (admin keys default to nil expiry). Status is one of "active",
// "grace", "revoked". RevokedAt mirrors ExpiresAt semantics. RotatedFromID
// is the predecessor key's id when this row was minted by rotateKey;
// empty otherwise.
//
// OrgID (issue #190 / IAM-6, PR 6): the org this key was minted against.
// Migration 00127 makes api_keys.org_id NOT NULL so every row carries
// a value — the legacy /v1/keys paths stamp it from the caller's
// personal org, and the org-scoped /v1/orgs/{slug}/keys paths stamp
// it from principal.Membership.OrgID. The field is backwards-compat
// ADD: existing SDK clients ignore unknown fields, and the schema is
// unchanged at the wire level except for the new property.
type APIKeyResponse struct {
	ID            string   `json:"id"`
	OrgID         string   `json:"org_id"`
	Prefix        string   `json:"prefix"` // "fp_live_abc12345…" (first 16 chars)
	Label         string   `json:"label,omitempty"`
	Scopes        []string `json:"scopes"`
	LastUsedAt    string   `json:"last_used_at,omitempty"`
	CreatedAt     string   `json:"created_at"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Status        string   `json:"status,omitempty"`          // "active"|"grace"|"revoked"
	RevokedAt     string   `json:"revoked_at,omitempty"`      // RFC3339
	RotatedFromID string   `json:"rotated_from_id,omitempty"` // predecessor key id
	// Plaintext appears ONLY on the create + rotate responses, never persisted.
	Plaintext string `json:"plaintext,omitempty"`
}

// RotateKeyResponse is the body of POST /v1/keys/{id}/rotate
// (issue #189 / IAM-5). Key is the new key (status='active'); Key is
// the loader-facing shape (id, prefix, label, scopes, status). The
// KeyPlaintext is returned exactly once; the old plaintext is
// NEVER returned (only the hash is stored). OldKeyExpiresAt is the
// grace deadline applied to the old key — the customer's CI rotates
// over by then. OldKeyID is the predecessor's id (matches Key.RotatedFromID).
type RotateKeyResponse struct {
	Key             APIKeyResponse `json:"key"`
	KeyPlaintext    string         `json:"key_plaintext"`
	OldKeyID        string         `json:"old_key_id"`
	OldKeyExpiresAt string         `json:"old_key_expires_at"` // RFC3339
}

// SetGraceWindowRequest is the body of
// PATCH /v1/account/keys/grace_window_days (issue #189 / IAM-5).
// Days is the per-account override for the rotation grace window
// (issue #189 / IAM-5). Days < 0 is rejected; Days == 0 means
// "atomic rotation" (no grace); a positive value is the number of
// days the old key remains usable after the new one is minted.
// Omit (or pass null) to clear the override and fall back to the
// plan default (api.DefaultAPIKeyGraceWindowDays = 7).
type SetGraceWindowRequest struct {
	Days *int `json:"days"`
}

// GraceWindowResponse is the body of GET /v1/account/keys/grace_window_days
// (issue #189 / IAM-5). Days is the per-account override; null when
// the account has no override (PlanDefault wins). PlanDefault is the
// plan-level default the rotation handler uses when the row is null
// — surfaced here so the dashboard can render "Plan default: 7 days"
// next to the override field.
type GraceWindowResponse struct {
	Days        *int `json:"days"`
	PlanDefault int  `json:"plan_default"`
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

// CreateOrgAPIKeyRequest is the body of POST /v1/orgs/{slug}/keys
// (issue #190 / IAM-6, PR 6). Mirrors CreateKeyRequest's shape so
// the SDK + dashboard can swap one request body for the other via
// the active-org hint. The handler validates Scopes against the
// same closed vocabulary (admin, apps:read, deploy:write,
// secrets:read, secrets:write, usage:read) — the only difference
// from CreateKeyRequest is the explicit org binding (so the
// developer role cannot silently escalate to a contractor key in
// another org via the admin-scope default).
type CreateOrgAPIKeyRequest struct {
	Label  string   `json:"label,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

// ListOrgAPIKeysResponse is the body of GET /v1/orgs/{slug}/keys
// (PR 6). Keys is sorted by created_at desc to match the legacy
// ListAPIKeys response ordering. The handler filters out revoked
// rows so the dashboard's "active deploy keys" view only counts
// keys the customer can still sign with — the "show revoked"
// toggle lives at the dashboard side, not here.
type ListOrgAPIKeysResponse struct {
	Keys []APIKeyResponse `json:"keys"`
}

// RotateOrgAPIKeyRequest is the body of
// POST /v1/orgs/{slug}/keys/{id}/rotate (PR 6). Label is the new
// label; empty means "inherit the old label" (the canonical
// handler shape, mirrors LegacyRotateKey). The org binding is
// inherited from the predecessor — rotation is org-local, never
// re-bound.
type RotateOrgAPIKeyRequest struct {
	Label string `json:"label,omitempty"`
}

// RotateOrgAPIKeyResponse is the body of
// POST /v1/orgs/{slug}/keys/{id}/rotate (PR 6). Same shape as
// RotateKeyResponse with org_id stamped on the new Key. The
// handler returns the legacy `key.rotated` + the new
// `api_key.rotated` audit events for one release cycle (PR 9
// drops the legacy event).
type RotateOrgAPIKeyResponse struct {
	Key             APIKeyResponse `json:"key"`
	KeyPlaintext    string         `json:"key_plaintext"`
	OldKeyID        string         `json:"old_key_id"`
	OldKeyExpiresAt string         `json:"old_key_expires_at"` // RFC3339
}

// CustomDomainResponse is a custom domain's wire shape. VerifiedAt is the
// zero time on unverified rows; the verifier goroutine polls DNS and updates
// it (spec §7).
//
// Issue #961 / Mega-A PR-3 additions: `Default` (true when this domain is the
// app's default), `CertNotAfter` (RFC3339 UTC string, populated on verified
// domains with an issued cert), and `CertSANs` (the cert's DNSNames subject
// alt names, useful for the `gregale domains show` listing). All three
// fields are omitempty so pre-PR-3 clients see bit-identical payloads.
type CustomDomainResponse struct {
	Domain         string   `json:"domain"`
	AppID          string   `json:"app_id"`
	ChallengeToken string   `json:"challenge_token,omitempty"`
	Verified       bool     `json:"verified"`
	VerifiedAt     string   `json:"verified_at,omitempty"`
	TXTRecord      string   `json:"txt_record,omitempty"` // convenience for the customer
	Default        bool     `json:"default,omitempty"`    // true when this domain is the app's default (issue #961 / Mega-A PR-3)
	CertNotAfter   string   `json:"cert_not_after,omitempty"`
	CertSANs       []string `json:"cert_sans,omitempty"`
	// CertStatus summarises the live cert dial (issue #961 / Mega-A PR-3
	// code-review round, MED-4). One of:
	//   ""           — cert dial was not attempted (domain unverified, or
	//                  dialCert not called because the cert is already known).
	//   "issued"     — port-443 handshake succeeded and the leaf cert
	//                  covers this domain (sanContains matched).
	//   "pending"    — domain is verified but the cert dial has not yet
	//                  succeeded (DNS propagated but cert not yet minted).
	//   "dial_failed:<reason>" — TCP dial, TLS handshake, or cert parse
	//                  failed. <reason> is one of: dial_refused,
	//                  dial_timeout, handshake_failed, no_peer_certs,
	//                  parse_failed. omitempty so legacy rows / pre-PR-3
	//                  clients see bit-identical payloads.
	CertStatus string `json:"cert_status,omitempty"`
}

// CreateCustomDomainRequest accepts a domain to bind.
type CreateCustomDomainRequest struct {
	Domain string `json:"domain"`
	AppID  string `json:"app_id"`
}

// DomainDoctorReport (ADR-120) is the wire shape for
// `GET /v1/domains/{domain}/doctor`. The 5-line shape mirrors
// the Render-style custom-domain check: dns_record_found,
// points_to_gregale, tls_certificate, caa_permits,
// ipv6_conflict. Each check carries its own status +
// remediation so the CLI can render "Set CNAME ... → ..." in
// the failure path.
//
// Stale=true means the cached observation row is older than
// FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) — the
// response is still 200, but the customer should re-poll.
// Healthy is a coarse summary (all checks ok OR na); the
// per-check Status is the source of truth.
//
// Check names use stable tokens (snake_case) so the CLI
// can grep / filter by name without parsing the human
// Detail field.
type DomainDoctorReport struct {
	Domain     string              `json:"domain"`
	AppID      string              `json:"app_id"`
	Stale      bool                `json:"stale,omitempty"`
	ObservedAt string              `json:"observed_at"` // RFC3339 UTC
	Healthy    bool                `json:"healthy"`
	Checks     []DomainDoctorCheck `json:"checks"`
}

// DomainDoctorCheck is one row of the doctor report.
// Remediation is the exact record to change when Status
// is "fail"; it's the load-bearing field for the
// activation drop-off. Observed carries the raw observed
// value (e.g. the actual CNAME target or the observed
// AAAA) so the customer can confirm their DNS state
// without leaving the CLI.
type DomainDoctorCheck struct {
	Name        string `json:"name"`                  // stable token: dns_record | points_to_gregale | tls_certificate | caa_permits | ipv6_conflict
	Status      string `json:"status"`                // ok | fail | pending | na
	Detail      string `json:"detail"`                // human-readable
	Observed    string `json:"observed,omitempty"`    // raw observed value
	Remediation string `json:"remediation,omitempty"` // exact record to change
	CheckedAt   string `json:"checked_at,omitempty"`  // RFC3339 UTC
}

// TenantSurfaceResponse is a tenant surface's wire shape (ADR-100 /
// issue #879). Hostnames carries the full list (verified +
// unverified) so the dashboard + CLI can render a single
// round-trip; TXTRecord is the convenience field the customer
// publishes at _faas-verify.<hostname>. Status / CertState mirror
// the state machine values (pending/active/suspended/deleted,
// none/pending/issued/failed) verbatim.
type TenantSurfaceResponse struct {
	ID            string                   `json:"id"`
	AccountID     string                   `json:"account_id"`
	AppID         string                   `json:"app_id"`
	Name          string                   `json:"name"`
	CertKind      string                   `json:"cert_kind"`
	Status        string                   `json:"status"`
	CertState     string                   `json:"cert_state"`
	CertNotAfter  string                   `json:"cert_not_after,omitempty"`
	CertLastError string                   `json:"cert_last_error,omitempty"`
	CreatedAt     string                   `json:"created_at"`
	UpdatedAt     string                   `json:"updated_at"`
	Hostnames     []TenantHostnameResponse `json:"hostnames"`
}

// TenantHostnameResponse is a hostname within a surface. Mirror
// shape of the unverified/verified columns on tenant_hostnames
// (post-PR-C the verifier flips Verified + VerifiedAt; v1
// surfaces the column shape now so the API contract doesn't shift
// when verification lands).
type TenantHostnameResponse struct {
	Hostname       string `json:"hostname"`
	ChallengeToken string `json:"challenge_token,omitempty"`
	Verified       bool   `json:"verified"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	TXTRecord      string `json:"txt_record,omitempty"` // convenience
}

// CreateTenantSurfaceRequest creates a tenant surface (one app, one
// cert, N hostnames). Hostnames is the seed list (can be empty
// initially; the customer POSTs additional hostnames via
// /v1/apps/{slug}/tenant-surfaces/{id}/hostnames). CertKind is
// optional and defaults to "per_host_san" when empty.
type CreateTenantSurfaceRequest struct {
	AppID     string   `json:"app_id"`
	Name      string   `json:"name"`
	CertKind  string   `json:"cert_kind,omitempty"`
	Hostnames []string `json:"hostnames,omitempty"`
}

// ListTenantSurfacesResponse paginates a /v1/apps/{slug}/tenant-surfaces
// listing. We use a flat array (no cursor) because the per-app
// dataset is bounded by the surface quota (Pro 5 / Scale 25 today);
// dashboards want the whole list to render a single component.
type ListTenantSurfacesResponse struct {
	Surfaces []TenantSurfaceResponse `json:"surfaces"`
}

// AddTenantHostnameRequest appends a hostname to an existing
// surface. Hostname is required and must be a unique FQDN
// (lowercase, RFC 1035 compliant); the store enforces the global
// UQ on tenant_hostnames.hostname.
type AddTenantHostnameRequest struct {
	Hostname string `json:"hostname"`
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
	// MinInstancesTarget (issue #557 / ADR-071) is the parent app's
	// effective min_instances at the time this instance was admitted.
	// Populated by apid's list-instances handler via
	// state.App.EffectiveMinInstances() (max of apps.min_instances
	// + ScalingPolicy.MinInstances). Zero is omitted — it means
	// "customer never opted into a floor". A pointer would surface
	// the difference between 0 and unset; the JSON contract uses
	// omitempty so absent and 0 are indistinguishable to clients.
	MinInstancesTarget int `json:"min_instances_target,omitempty"`
}

// ListInstancesResponse is the page shape for GET /v1/instances
// (issue #393). Cursor is the instances.id UUIDv7 — the handler
// emits the last row's id as NextBefore when len(Instances) == limit,
// so the caller can pass it back unchanged as ?before=<id> on the
// next request. Empty NextBefore means the page is the end. An
// account with zero live instances returns 200 with an empty
// `instances` array — never 404.
type ListInstancesResponse struct {
	Instances  []InstanceResponse `json:"instances"`
	NextBefore string             `json:"next_before,omitempty"`
}

// UsageResponse is one app's monthly usage slice (spec §10).
//
// CPUUsageUsec is the cumulative host cgroup CPU-µs this app
// consumed during the month (issue #279 / PR-B). Measurement
// only — billing is on plan RAM (spec §4.7). Exposed as
// cpu_usec (integer) so the dashboard can compute hours lazily
// without an integer→float→integer round trip; the SDK exposes
// both the raw integer and the derived CPUHours getter.
type UsageResponse struct {
	AppID     string `json:"app_id"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
	// IncludedGBHours is the included quota for the account's plan at the
	// requested month; the CLI computes the overage from this and the rows.
	IncludedGBHours int64 `json:"included_gb_hours"`
	// CPUUsageUsec is the per-app monthly CPU-µs — informational
	// only (issue #279 / PR-B). 0 when no meterd sample has
	// accumulated yet (boot, or the schedd reader has no row for
	// this app).
	CPUUsageUsec int64 `json:"cpu_usec"`
	// TXBytes (ADR-046, step 10) is the per-app monthly
	// HTTP-response byte delta — informational only. Source:
	// gateway statusRecorder.Bytes → meterd SampleAndRoll →
	// usage_minutes.tx_bytes. Not billed (ADR-046 §6); the
	// gateway-side producer. 0 when no meterd sample has
	// accumulated yet.
	TXBytes int64 `json:"tx_bytes"`
	// NetTxBytes (ADR-046, step 10) is the per-app monthly
	// byte delta on root-side vethHost.rx_bytes —
	// informational only. Source: vmmd netstats.Cache →
	// schedd instancestats.Poller → schedd
	// ListInstanceStats → meterd SampleAndRoll →
	// usage_minutes.net_tx_bytes. Not billed (ADR-046 §6).
	// 0 when no meterd sample has accumulated yet.
	NetTxBytes int64 `json:"net_tx_bytes"`
	// NetRxBytes (ADR-048) is the per-app monthly byte
	// delta on root-side vethHost.tx_bytes — mirror of
	// NetTxBytes on the ingress direction (root → guest).
	// Source: vmmd netstats.Cache TX path → schedd
	// instancestats.Poller → meterd SampleAndRoll →
	// usage_minutes.net_rx_bytes. Informational only —
	// not billed (ADR-048 §5). 0 when no meterd sample has
	// accumulated yet.
	NetRxBytes int64 `json:"net_rx_bytes"`
	// ColdBootCount (ADR-048) is the per-app monthly
	// count of customer requests whose authoritative wake outcome
	// was WAKE_COLD_BOOT. Source: gatewayd's minute-bucketed
	// usage stream → meterd SampleAndRoll → usage_minutes.
	// cold_boot_count. Informational only — not billed.
	// 0 when no meterd sample has accumulated yet.
	ColdBootCount int64 `json:"cold_boots"`
}

// CPUHours returns CPUUsageUsec converted to CPU-hours. 1 hour
// = 3.6e9 µs. Convenience getter for the SDK and the CLI; the
// dashboard can compute the same value with `pkg/meter.CPUHours`.
func (u UsageResponse) CPUHours() float64 {
	return float64(u.CPUUsageUsec) / 3.6e9
}

// TotalEgressGB returns (TXBytes + NetTxBytes) converted to GB
// (1 GB = 1024^3 bytes).
//
// IMPORTANT (ADR-046, PR-414 I5): the value INCLUDES Ethernet
// framing (~14 + 20 bytes per packet) because net_tx_bytes
// reads the kernel `/sys/class/net/<vethHost>/statistics/rx_bytes`
// counter — interface bytes, not IP-payload bytes. A 1 GB HTTP
// workload can show as ~1.2-1.5 GB on this counter. The two
// columns are exposed separately so callers can distinguish
// gateway response bytes (HTTP only, exact) from netns tap0
// egress (HTTP + 80/443/53 + DNS, includes framing).
//
// For HTTP-payload-only bytes, callers should use TXBytes
// directly (do not divide by 1 GiB and call it "egress GB").
// The future billing PR will pick the unit; this convenience
// getter exists so the SDK and the CLI have a single
// "all-bytes" surface for informational dashboards.
//
// Convention:
//   - TotalEgressGB = interface bytes, includes framing.
//   - TXBytes = HTTP response bytes, exact.
//   - NetTxBytes = interface bytes on root-side vethHost.rx_bytes.
func (u UsageResponse) TotalEgressGB() float64 {
	return float64(u.TXBytes+u.NetTxBytes) / (1024 * 1024 * 1024)
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

// --- Invoice history (issue #259) -----------------------------------------

// Invoice is one persisted invoice from a billing provider, surfaced
// via GET /v1/invoices. Money is integer cents in the provider's
// currency (the financial model distills to EUR at the API edge).
// PDFAvailable is the only PDF surface we expose — the hosted PDF URL
// is provider-scoped and customer-fetched via the Stripe/Paddle
// portal, not via this API. HostedURL is intentionally not on the
// wire; see state.Invoice for the rationale.
type Invoice struct {
	ID                string    `json:"id"`
	Provider          string    `json:"provider"`
	ProviderInvoiceID string    `json:"provider_invoice_id"`
	Number            string    `json:"number"`
	Status            string    `json:"status"`
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	SubtotalCents     int64     `json:"subtotal_cents"`
	TaxCents          int64     `json:"tax_cents"`
	TotalCents        int64     `json:"total_cents"`
	AmountPaidCents   int64     `json:"amount_paid_cents"`
	Currency          string    `json:"currency"`
	PDFAvailable      bool      `json:"pdf_available"`
	CreatedAt         time.Time `json:"created_at"`
}

// InvoiceListResponse is the page shape for GET /v1/invoices.
// Items is the page (in period_end DESC, id DESC order). NextBefore
// is the cursor the caller passes on the next request to fetch the
// older page. Empty NextBefore means the page is the end. Empty
// Items with 200 OK is the empty-history shape — never 404.
type InvoiceListResponse struct {
	Items      []Invoice `json:"items"`
	NextBefore string    `json:"next_before,omitempty"`
}

// --- Account credits (issue #279) -----------------------------------------

// AccountCreditResponse is the wire shape for one row in
// account_credits. Cents is integer (CLAUDE.md: never float on money).
// ExpiresAt is RFC 3339 when set; empty when the credit has no
// expiry. CreatedAt is the issuance timestamp. The handler echoes the
// row back to the operator on POST /v1/admin/accounts/{id}/credits and
// on GET /v1/admin/accounts/{id}/credits (list, when it lands).
type AccountCreditResponse struct {
	ID             string     `json:"id"`
	AccountID      string     `json:"account_id"`
	CentsRemaining int64      `json:"cents_remaining"`
	Reason         string     `json:"reason"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// ConsumeInvoiceResponse is the wire shape returned by the credit
// consumption reducer on POST /v1/invoices/{id}/consume-credits
// (issue #279 PR-C). ConsumedCents is the integer cents of overage
// that were drained against this invoice (floored to whole cents).
// RemainingCreditsCents is the sum of cents_remaining across the
// account's active credits after the call. AlreadyConsumedForInvoice
// is true on idempotent replays (e.g. webhook redelivery) — the
// reducer returns the same ConsumedCents without double-decrementing.
// PerCredit mirrors the per-credit delta rows so the operator can
// see FIFO drain order. Money is integer cents (CLAUDE.md).
type ConsumeInvoiceResponse struct {
	InvoiceID                 string              `json:"invoice_id"`
	ConsumedCents             int64               `json:"consumed_cents"`
	RemainingCreditsCents     int64               `json:"remaining_credits_cents"`
	AlreadyConsumedForInvoice bool                `json:"already_consumed_for_invoice"`
	PerCredit                 []ConsumedCreditRow `json:"per_credit"`
}

// ConsumedCreditRow is one entry in ConsumeInvoiceResponse.PerCredit.
// NewBalance is cents_remaining after the decrement — 0 means the
// credit was fully drained, > 0 means a partial drain.
type ConsumedCreditRow struct {
	CreditID   string `json:"credit_id"`
	DeltaCents int64  `json:"delta_cents"`
	NewBalance int64  `json:"new_balance"`
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

// AuthCapabilities is the body of GET /v1/auth/capabilities
// (issue #419 / ADR-046). The dashboard reads this on /login to
// decide whether to render the "Sign in with Google" / "Sign in
// with GitHub" buttons. Each per-provider entry reports whether
// the consent route is wired (Enabled == true) or whether it would
// 503 with oauth_provider_unavailable because both ID+SECRET were
// unset at boot.
//
// The set of provider names is closed; new providers land as new
// keys, not as a list. The Wire-shape deliberately keeps the keys
// named (`providers.google`, `providers.github`) so the dashboard
// template can reach them directly via {{.Auth.GoogleEnabled}}-
// style guards, and the spec_compliance_test (cmd/apid/spec_compliance_test.go)
// can pin the schema parity.
type AuthCapabilities struct {
	Providers AuthProviders `json:"providers"`
}

// AuthProviders is the per-provider capability map. Closed set
// (google, github) — handlers MUST add a new field here when
// adding a new provider, not relax this to map[string]… .
type AuthProviders struct {
	Google OAuthProviderCapability `json:"google"`
	GitHub OAuthProviderCapability `json:"github"`
}

// OAuthProviderCapability is one provider's capability flag.
// Source is auth.SignInProvider.Enabled() — the boot-resolved
// state loaded once at apid startup and pinned for the process
// lifetime.
type OAuthProviderCapability struct {
	Enabled bool `json:"enabled"`
}

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
// The two endpoints differ only in semantics — signup is
// create-or-idempotent-on-existing; login is just sign-in. Wire
// shape is identical.
//
// Email is echoed back so the CLI's finalizeLogin step can render
// "Logged in as <email> (<plan> plan)" without an extra Whoami
// round-trip. The cookie /signup and /login paths leave Email
// out because the dashboard already knows the email of the
// session owner; the programmatic path has no session, so we
// stamp the request email onto the response.
type ProgrammaticAuthResponse struct {
	AccountID string             `json:"account_id"`
	Email     string             `json:"email"`
	Plan      string             `json:"plan"`
	APIKey    ProgrammaticAPIKey `json:"api_key"`
}

// ProgrammaticAPIKey is the one-shot surface for the freshly-minted
// api_key returned alongside a successful programmatic signup or
// login. The plaintext is shown ONCE — callers persist it through
// the CLI's saveToken() path immediately. Prefix is the leading
// "fp_live_" + 8-character identifier that the dashboard uses for
// greppable display (mirrors the /v1/keys display shape).
type ProgrammaticAPIKey struct {
	Plaintext string `json:"plaintext"`
	Prefix    string `json:"prefix"`
	ID        string `json:"id"`
}

// MagicLinkSignupRequest is the body of POST /v1/auth/signup/magic-link.
// Anti-enumeration: the server always returns 200 with an identical
// body regardless of whether the email is bound, so the surface
// cannot be used to enumerate accounts.
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

// DailyUsagePoint is one day in the account's trailing usage trend.
// GBHours is the sum across apps; TopAppSlug identifies the largest
// contributor for that day. All fields are informational and use the
// same GB-hour conversion as UsageSummaryResponse.
type DailyUsagePoint struct {
	Date          string  `json:"date"` // YYYY-MM-DD in UTC
	GBHours       float64 `json:"gb_hours"`
	TopAppSlug    string  `json:"top_app_slug,omitempty"`
	TopAppGBHours float64 `json:"top_app_gb_hours,omitempty"`
}

// UsageSummaryResponse is the roll-up for the current month (or any
// month passed as a query param). Used by the dashboard usage page so
// the customer sees the account summary and its trailing daily trend
// without having to sum rows.
//
// Overage math: anything above IncludedGBHours is billable at the
// overage rate in the financial model (€0.01/GB-h). Cents are integer.
//
// Issue #279 / PR-B: UsedCPUHours is informational and NOT billed.
// The billing math is on UsedGBHours (plan RAM + 8 MB per running
// second). The CPU dimension is a measurement the dashboard will
// surface in a separate panel without affecting the billing total.
//
// ADR-046 (step 10): UsedEgressGB is informational and NOT
// billed. The two egress columns (tx_bytes + net_tx_bytes) are
// exposed separately at the per-app UsageResponse level; the
// summary rolls them up for the dashboard's single-number
// panel.
type UsageSummaryResponse struct {
	Month           string  `json:"month"`             // YYYY-MM
	UsedGBHours     float64 `json:"used_gb_hours"`     // Σ mb_seconds / 1024 / 3600
	IncludedGBHours int64   `json:"included_gb_hours"` // from plan limits
	OverageGBHours  float64 `json:"overage_gb_hours"`  // max(0, used - included)
	OverageCents    int64   `json:"overage_cents"`     // overage * 1.0 (€0.01/GB-h in cents)
	// UsedCPUHours is the per-month CPU-hours Σ CPUUsageUsec /
	// 3.6e9. Informational only — billing is on UsedGBHours.
	// Issue #279 / PR-B. The customer dashboard renders this
	// alongside the other account summary dimensions.
	UsedCPUHours float64 `json:"used_cpu_hours"`
	// UsedEgressGB is the per-month egress Σ (TXBytes +
	// NetTxBytes) / 1024^3. Informational only — not
	// billed (ADR-046 §6). The two columns are exposed
	// separately at the per-app level; this is the
	// single-number roll-up for the dashboard's
	// "egress this month" panel.
	UsedEgressGB float64 `json:"used_egress_gb"`
	// UsedIngressGB (ADR-048) is the per-month ingress Σ
	// NetRxBytes / 1024^3. Informational only — not billed
	// (ADR-048 §5). Same Ethernet-framing caveat as
	// UsageResponse.TotalEgressGB. The dashboard's "ingress
	// this month" panel reads this single number; the
	// per-app breakdown lives at UsageResponse.NetRxBytes.
	UsedIngressGB float64 `json:"used_ingress_gb"`
	// ColdBootTotal (ADR-048) is the per-month Σ of
	// customer requests whose wake outcome was WAKE_COLD_BOOT
	// across every app on this account. Informational only — not
	// billed. The dashboard's "this customer's cold-boot
	// bill of health" panel reads this single number; the
	// per-app breakdown lives at UsageResponse.ColdBootCount.
	ColdBootTotal int64 `json:"cold_boots"`
	// Daily is the trailing 30 UTC calendar days of account usage,
	// grouped by day. It is additive so existing clients can ignore it.
	Daily []DailyUsagePoint `json:"daily"`
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

// ValidateAppCPUMillicores validates the closed set supported by the first
// configurable CPU release.
func ValidateAppCPUMillicores(cpuMillicores int) *Problem {
	if ValidAppCPUMillicores(cpuMillicores) {
		return nil
	}
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidAppCPU,
		"Invalid CPU", "cpu_millicores must be one of: 250, 500, 1000")
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
// CPUUsageUsec is the per-app monthly CPU-µs — informational
// only (issue #279 / PR-B). 0 when no meterd sample has
// accumulated.
type UsageExportResponse struct {
	AppID        string `json:"app_id"`
	Month        string `json:"month"`
	MBSeconds    int64  `json:"mb_seconds"`
	Requests     int64  `json:"requests"`
	CPUUsageUsec int64  `json:"cpu_usec"`
	// ADR-046 (step 10): per-app monthly egress bytes —
	// informational only, not billed. Mirrors the new
	// UsageResponse.TXBytes / UsageResponse.NetTxBytes fields
	// (the export bundle and the API shape stay in lockstep).
	// The gateway-side tx_bytes producer lands in PR-2.
	TXBytes    int64 `json:"tx_bytes"`
	NetTxBytes int64 `json:"net_tx_bytes"`
	// ADR-048: mirror of UsageResponse.NetRxBytes /
	// UsageResponse.ColdBootCount on the export surface.
	// Informational only — not billed.
	NetRxBytes    int64 `json:"net_rx_bytes"`
	ColdBootCount int64 `json:"cold_boots"`
}

// DailyUsageResponse is one row of GET /v1/usage/daily — the
// per-(account, app, day) rollup the dashboard's hot path reads
// (migrations/00067_extend_metering_telemetry.sql::usage_daily).
// Distinct from UsageResponse which is the per-app monthly
// rollup (pkg/state.UsageByMonth); the daily route is for
// yesterday / today / single-day queries where the monthly grain
// is over-aggregated. ADR-048 §5.
type DailyUsageResponse struct {
	AppID          string `json:"app_id"`
	Day            string `json:"day"` // YYYY-MM-DD
	MBSeconds      int64  `json:"mb_seconds"`
	Requests       int64  `json:"requests"`
	CPUUsageUsec   int64  `json:"cpu_usec"`
	TXBytes        int64  `json:"tx_bytes"`
	NetTxBytes     int64  `json:"net_tx_bytes"`
	NetRxBytes     int64  `json:"net_rx_bytes"`
	ColdBootCount  int64  `json:"cold_boots"`
	BuilderSeconds int64  `json:"builder_seconds"`
}

// CPUHours returns CPUUsageUsec converted to CPU-hours. Mirror
// of UsageResponse.CPUHours so the export bundle and the API
// shape stay in lockstep.
func (u UsageExportResponse) CPUHours() float64 {
	return float64(u.CPUUsageUsec) / 3.6e9
}

// DailyUsageListResponse is the page shape for GET /v1/usage/daily.
// Mirrors the invoice / deployment list shapes — Items is always
// non-nil so the JSON encodes an empty array, not null, when the
// requested day has no rollup rows yet (ADR-048 §5).
type DailyUsageListResponse struct {
	Items []DailyUsageResponse `json:"items"`
}

// StorageUsageResponse is one row of GET /v1/usage/storage — the
// per-(account, app, day) storage rollup (migrations/
// 00070_snapshot_storage_daily.sql). Mirrors the snapshot+layer
// byte totals that the meterd storage rollup cron (pkg/meter/
// storage.go) populates. ADR-049 §B.3.
type StorageUsageResponse struct {
	AppID         string `json:"app_id"`
	Day           string `json:"day"` // YYYY-MM-DD
	SnapshotBytes int64  `json:"snapshot_bytes"`
	LayerBytes    int64  `json:"layer_bytes"`
}

// StorageUsageListResponse is the page shape for GET /v1/usage/storage.
// Items is always non-nil so the JSON encodes an empty array, not
// null, when the requested day has no rollup rows yet.
type StorageUsageListResponse struct {
	Items []StorageUsageResponse `json:"items"`
}

// BillingPortalResponse is the wire shape for GET /v1/billing/portal
// (issue #253, extended in issue #242). URL is the operator-configured
// billing portal link — today: FAAS_BILLING_PORTAL_URL with
// `{account_id}` substituted. Empty URL is a 200 (the request itself
// succeeded); it is the "absent" sentinel meaning the box has no
// portal configured and the CLI should print a friendly hint instead
// of opening the browser to "". The field is omitempty so an unset URL
// on a Free account does not surface as JSON null in either the
// dashboard's SSR page or the SDK response.
//
// PaymentMethod (issue #242) carries the canonical card-on-file summary
// so the CLI's `faas billing payment-method` subcommand and the
// dashboard's billing page both render from the same round-trip.
// Field is omitempty; Free / no-card-on-file responses carry no
// payment_method key.
type BillingPortalResponse struct {
	URL           string                `json:"url,omitempty"`
	PaymentMethod *PaymentMethodSummary `json:"payment_method,omitempty"`
}

// PaymentMethodSummary is the provider-agnostic card-on-file summary
// (issue #242). Both Stripe (PaymentMethods.List) and Paddle
// (Customer.Get carries payment_method) reduce to this shape at the
// provider boundary in pkg/billing/; the conversion lives behind
// billing.Provider.PaymentMethodSummary so neither provider's
// internal field names leak onto the wire. Brand is the lowercase
// card brand (visa, mastercard, amex, …) per the Stripe convention
// Paddle mirrors; empty when unknown.
//
// Last4 is the trailing 4 digits of the PAN. ExpMonth / ExpYear carry
// the card expiry as integers (1..12 / 4-digit). Zero values are the
// "unknown" sentinel — Free / no-card-on-file clients see all-zero
// fields; the CLI renders the zero as the "no payment method on file"
// CTA.
type PaymentMethodSummary struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
}

// BillingRetryResponse is the wire shape for POST /v1/billing/retry
// (issue #242). AttemptID is the provider-side handle for the new
// charge attempt (Stripe `in_…` invoice id; Paddle `txn_…` transaction
// id). ProviderRefID is the underlying payment-intent id or merchant
// transaction reference for ops debugging. Status is the provider's
// last-known status at the time of the call — the Stripe / Paddle
// webhook will fill in the final state asynchronously. NextBillingAt
// is the next scheduled billing-cycle timestamp (RFC 3339); null when
// the retry does not advance the cycle.
//
// All integer money paths use int64 millicents at the SDK boundary
// (pkg/billing/), but this DTO does not carry money — the amount was
// already locked in at the original subscription or charge. Currency
// comes from the provider's catalog, not the retry call.
type BillingRetryResponse struct {
	AttemptID     string     `json:"attempt_id"`
	ProviderRefID string     `json:"provider_ref_id"`
	Status        string     `json:"status"`
	NextBillingAt *time.Time `json:"next_billing_at"`
}

// BillingCancelResponse is the wire shape for POST /v1/billing/cancel
// (issue #242). The cancel is scheduled, not immediate — the account
// keeps the current plan until `effective_at`, then downgrades to Free
// on the next dunning tick. EffectiveAt is Stripe's `current_period_end`
// or the account's period-end timestamp on Paddle; rendered in --json
// as an RFC 3339 string. CancelScheduled is always true on 200 (the
// HTTP 200 is itself the contract) but the field is preserved so
// --json scripts can branch on it without parsing the response status.
type BillingCancelResponse struct {
	CancelScheduled bool      `json:"cancel_scheduled"`
	EffectiveAt     time.Time `json:"effective_at"`
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
//
// ADR-092 PR-B: the export lists every scope per app (not just
// default-scope). Scope is required so a customer's import into a
// fresh install lands each row at the same scope it was sealed at;
// pre-PR-B this field was missing and on import the row would have
// collapsed to default scope, silently overwriting any prod/staging
// rows on the destination.
type AppSecretExportResponse struct {
	AppID      string `json:"app_id"`
	Scope      string `json:"scope"` // ADR-092 PR-B
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

// InvocationReplayResponse is the 202-side of POST
// /v1/invocations/{id}/replay. The wire is identical to
// AsyncInvokeResponse — the replay creates a new async invocation
// against the original's app/instance, so the read-side contract is
// the same id + status_url the customer already polls. Aliased so
// future divergence (e.g. an "original_id" field for dedup) has a
// single place to grow without touching every SDK call site.
type InvocationReplayResponse = AsyncInvokeResponse

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
	// DeadlineAt (ADR-134 PR-B): optional hard-stop. Must be
	// within (now + Limits.MaxAsyncInvocationDeadlineSeconds) or
	// the handler rejects with invalid_deadline_at.
	DeadlineAt *time.Time `json:"deadline_at,omitempty"`
	// RetryPolicy (ADR-134 PR-B): optional per-row override of
	// the plan-default retry curve. Stored verbatim in
	// invocations.retry_policy JSONB; the drain decodes it
	// through dispatch.RetryPolicy.
	RetryPolicy *RetryPolicyDTO `json:"retry_policy,omitempty"`
	// RetentionSeconds (ADR-134 PR-B): optional override of the
	// plan-default retention horizon. 0 means "use the plan
	// default" (Limits.MaxAsyncResultRetentionSeconds); any
	// positive integer sets invocations.result_retention_until =
	// completed_at + RetentionSeconds.
	RetentionSeconds *int `json:"retention_seconds,omitempty"`
}

// RetryPolicyDTO is the wire shape for dispatch.RetryPolicy. Lives
// in pkg/api so the SDK can type the override without importing
// pkg/dispatch directly; the handler decodes the DTO into a
// dispatch.RetryPolicy before persisting to JSONB.
type RetryPolicyDTO struct {
	MaxAttempts   int     `json:"max_attempts,omitempty"`
	BaseSeconds   float64 `json:"base_seconds,omitempty"`
	MaxSeconds    float64 `json:"max_seconds,omitempty"`
	JitterSeconds float64 `json:"jitter_seconds,omitempty"`
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
	// DeadlineAt (ADR-134 PR-B): optional hard-stop. The drain
	// transitions the row to dead_letter when this time passes.
	DeadlineAt *time.Time `json:"deadline_at,omitempty"`
	// RetryPolicyJSON (ADR-134 PR-B): optional JSON-encoded
	// dispatch.RetryPolicy. Lazy-decoded by the drain via
	// state.Invocation.RetryPolicy().
	RetryPolicyJSON json.RawMessage `json:"retry_policy,omitempty"`
	// ResultRetentionUntil (ADR-134 PR-B): optional explicit
	// retention horizon. NULL means "use the plan default"
	// (Limits.MaxAsyncResultRetentionSeconds).
	ResultRetentionUntil *time.Time `json:"result_retention_until,omitempty"`
	// LastReplayedAt (ADR-134 PR-C): when this row was most
	// recently replayed from a dead_letter parent via
	// POST /v1/apps/{slug}/queues/dead_letter/{id}/replay. NULL
	// until the first replay.
	LastReplayedAt *time.Time `json:"last_replayed_at,omitempty"`
}

// ListInvocationsResponse is the wire shape for GET /v1/invocations.
// The handler emits a `[]state.Invocation` under the `invocations`
// key; here we declare the same shape with the SDK-side mirror type
// so pkg/api stays decoupled from pkg/state.
type ListInvocationsResponse struct {
	Invocations []Invocation `json:"invocations"`
}

// --- Issue #791 — cron run history ----------------------------------

// CronRunOutcome is the normalized result of a single cron fire. It
// mirrors state.InvocationOutcome plus the synthetic "running" value
// the API substitutes for a NULL outcome, so a client never has to
// handle an empty string.
type CronRunOutcome string

const (
	// CronRunSuccess — the fire completed.
	CronRunSuccess CronRunOutcome = "success"
	// CronRunFailed — the fire failed permanently for a reason other
	// than a deadline.
	CronRunFailed CronRunOutcome = "failed"
	// CronRunTimeout — the fire exceeded its deadline (gateway 504 or
	// an expired dispatch lease).
	CronRunTimeout CronRunOutcome = "timeout"
	// CronRunDeadLetter — the per-plan retry budget was exhausted.
	CronRunDeadLetter CronRunOutcome = "dead_letter"
	// CronRunRunning — the fire is still in flight (the underlying
	// invocation row is non-terminal and carries no outcome).
	CronRunRunning CronRunOutcome = "running"
)

// CronRun is one row of a cron's execution history: GET
// /v1/crons/{id}/runs.
//
// Deliberately NOT the full Invocation shape. A cron run is a narrow
// question — did it work, when, and for how long — and the caller
// should not have to know that runs are stored as invocations, nor
// subtract two timestamps to get a duration. Keeping the projection
// separate also means the invocations row can gain or lose fields
// without churning the cron surface.
type CronRun struct {
	ID string `json:"id"`
	// StartedAt is the underlying invocation's created_at — when the
	// cron fired, not when the app began executing.
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// DurationMs is completed_at - started_at, computed server-side.
	// nil while the run is still in flight.
	DurationMs *int64         `json:"duration_ms,omitempty"`
	Outcome    CronRunOutcome `json:"outcome"`
	// Attempts is the dispatch count; > 1 means the row was retried.
	Attempts   int    `json:"attempts"`
	InstanceID string `json:"instance_id,omitempty"`
	// Error is the operator-facing failure text. Unstructured and
	// unversioned — branch on Outcome, never on this string.
	Error string `json:"error,omitempty"`
}

// ListCronRunsResponse is the wire shape for GET /v1/crons/{id}/runs.
// Ordered newest-first; page with ?before=<id of the last row>.
type ListCronRunsResponse struct {
	Runs []CronRun `json:"runs"`
}

// FireCronResponse is the 202 body for POST /v1/crons/{id}/run
// (ADR-090 PR-C). The endpoint is asynchronous — apid inserts a
// pending row into cron_fire_now_requests and emits db.NotifyCronRunNow,
// then returns 202 immediately. schedd's fire-now consumer
// (pkg/sched/fire_now.go) processes the row in its own process and
// stamps the terminal status.
//
// RequestID is the cron_fire_now_requests.id; clients use it to
// poll the row's status (future GET /v1/cron-fire-now-requests/{id})
// or to correlate the audit-event stream (`cron.fired.manually`)
// back to their request. Status starts at "pending" — terminal
// values are "succeeded" or "failed".
type FireCronResponse struct {
	RequestID string `json:"request_id"`
	CronID    string `json:"cron_id"`
	Status    string `json:"status"`
}

// FireCronRequestResponse is the read shape for the row that backs
// `GET /v1/cron-fire-now-requests/{request_id}` (issue #791 PR-D /
// ADR-090 §Sub-decision 7). Nullable fields are *string so a pending
// row does NOT serialise a zero timestamp as a literal
// "0001-01-01T00:00:00Z".
//
// Polling contract: clients should poll until Status is one of the
// terminal values {succeeded, failed, cancelled}. The schedd fire-now
// consumer populates FinishedAt + Error + InvocationID at terminal stamp.
type FireCronRequestResponse struct {
	RequestID    string  `json:"request_id"`
	CronID       string  `json:"cron_id"`
	Status       string  `json:"status"`
	RequestedAt  string  `json:"requested_at"`          // RFC3339Nano UTC
	FinishedAt   *string `json:"finished_at,omitempty"` // RFC3339Nano UTC or null
	InvocationID *string `json:"invocation_id,omitempty"`
	Error        *string `json:"error,omitempty"`
	AccountID    string  `json:"account_id"`
}

// --- Issue #394 — queue introspection -------------------------------
//
// QueueStateResponse is the read-only depth/stats contract for
// GET /v1/apps/{slug}/queues/state. NO lease is acquired by calling
// this endpoint. PlanCap is the static per-plan MaxQueueDepth so
// dashboards can render "depth / cap" without a second lookup.
//
// Wire-shape note on plan downgrades: PlanCap reflects the *current*
// account plan's cap at read time. After a downgrade (e.g. Pro
// MaxQueueDepth=25 → Free MaxQueueDepth=0), a customer whose queue
// has not yet drained will see `Plan: "free"` + `PlanCap: 0` +
// `Depth: <5-or-whatever>` — a "you have messages but no cap" wire
// shape. The dashboard surface should display the post-downgrade
// `PlanCap` as the *enforceable* cap and surface "over limit after
// downgrade" if `Depth > PlanCap`. Documented in the README so the
// dashboard team knows to mirror it.
//
// OldestPendingAt / OldestPendingAgeSeconds are omitted when the queue
// is empty (zero value); clients should treat absence as "no backlog".
type QueueStateResponse struct {
	AppSlug                 string     `json:"app_slug"`
	Plan                    string     `json:"plan"`
	PlanCap                 int        `json:"plan_cap"`
	Depth                   int        `json:"depth"`
	InFlight                int        `json:"in_flight"`
	OldestPendingAt         *time.Time `json:"oldest_pending_at,omitempty"`
	OldestPendingAgeSeconds *int64     `json:"oldest_pending_age_seconds,omitempty"`
	GeneratedAt             time.Time  `json:"generated_at"`
}

// QueuePeekMessage is one pending row returned by GET .../queues/peek.
// The handler does NOT acquire a lease and does NOT increment attempts —
// repeated peeks leave the underlying state byte-identical. Payload
// is rendered as a JSON string (the stored column is jsonb, surfaced
// verbatim) so callers can decode with their preferred JSON lib.
//
// LastError omits when the row has not yet failed (most rows in a
// healthy queue). Pending rows can carry a last_error if they were
// transiently failed and re-queued before being claimed again.
type QueuePeekMessage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Attempts  int       `json:"attempts"`
	Payload   string    `json:"payload"`
	LastError string    `json:"last_error,omitempty"`
}

// QueuePeekResponse is the paginated contract. NextBefore is the
// id (UUID) of the LAST row in the returned page — invariant across
// endpoints: it is always "rows[len-1].ID in the order returned", not
// an anchor in some sort direction. Pass it as `?before=<id>` on the
// next call. Empty NextBefore means "no more pages" (caller stops).
// Caveat: NextBefore being present does NOT guarantee more rows
// exist — if the underlying table has exactly `limit` rows, the
// handler emits NextBefore and the next request returns empty.
// Clients must continue until NextBefore is absent on an empty list.
type QueuePeekResponse struct {
	AppSlug    string             `json:"app_slug"`
	Messages   []QueuePeekMessage `json:"messages"`
	NextBefore string             `json:"next_before,omitempty"`
}

// QueueDeadLetterMessage is one row that exhausted its plan's retry
// budget (state='dead_letter'). FailedAt is the moment the drain
// transitioned the row to terminal (== state.Invocation.CompletedAt).
// LastError is the most recent failure; Payload is preserved verbatim
// so an operator can replay it as a fresh send if needed.
//
// LastError has no omitempty: a dead-letter row that exhausted its
// retry budget ALWAYS carries a last_error (that's what dead-letter
// means). An absent last_error here would be a bug — pin it as
// required so a regression that drops it surfaces at PR review.
type QueueDeadLetterMessage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	FailedAt  time.Time `json:"failed_at"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	Payload   string    `json:"payload"`
}

// QueueDeadLetterResponse is the paginated contract for
// GET /v1/apps/{slug}/queues/dead_letter. Same cursor convention as
// QueuePeekResponse (NextBefore = last id, `?before=<id>` for the next
// page). Rows are ordered newest-first (created_at DESC) so operators
// see the most recent failures at the top.
type QueueDeadLetterResponse struct {
	AppSlug    string                   `json:"app_slug"`
	Messages   []QueueDeadLetterMessage `json:"messages"`
	NextBefore string                   `json:"next_before,omitempty"`
}

// --- IAM-4 (ADR-035) — auth audit event surface -----------------------------
//
// AuditEventResponse is one row of the customer's own security event
// timeline. The kind taxonomy is documented in
// docs/adr/035-auth-audit-events.md; common values include
// "auth.login", "auth.logout", "key.created", "key.deleted",
// "secret.set", "secret.deleted", "secret.rotated",
// "account.plan_changed",
// "account.deletion_scheduled", "account.deletion_restored".
//
// ADR-089 PR-A: "secret.rotated" joins the kind vocabulary. It is
// emitted by the per-secret rotate handler (PR-B) when the row
// already had a value (rotation, not first-time set) and by
// pkg/rekey.Replayer when the background re-seal pass rewrites a
// row's ciphertext. The two cases are distinguished by the
// audit_log.actor column ("apid" for user-initiated, "rekey"
// for background) — see ADR-089 D2.
//
// Subject is the account_id the event was recorded against (string,
// not the raw uuid UUID type — pkg/api stays string-typed for wire
// stability). Data is the raw jsonb the apid auditor wrote; the schema
// varies by kind and is documented per-kind in the ADR.
type AuditEventResponse struct {
	ID      string `json:"id"`    // bigint as string
	At      string `json:"at"`    // RFC 3339
	Actor   string `json:"actor"` // "apid" today; "schedd" for state-transition events
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"` // account_id (uuid string form)
	// Severity (Mega-PR B) is the highest-severity classification
	// for stateless.advisory rows; "" for all other kinds. Carried
	// at the top level so an SDK consumer can triage rows without
	// re-parsing the data JSONB blob — the data.severity field is
	// still the canonical storage shape, but the SDK shouldn't have
	// to know the kind-specific schema to learn the severity.
	// omitempty: pre-PR-427 rows and non-stateless kinds render
	// with no Severity field at all (backwards-compatible wire).
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

// --- audit_log (issue #755 / PR-6) ---------------------------------------
//
// AuditLogEntry is the wire shape for one row of the FK-free audit_log
// table (migrations/00163_audit_log.sql). Distinct from
// AuditEventResponse on three load-bearing axes:
//
//   - ID is a UUID, not a bigint — the table uses uuid.UUID PK so the
//     row outlives a deleted accounts row.
//   - AccountID is a UUID rendered as a canonical-hyphenated string
//     (matches uuid.UUID.String()). It is omitempty: anonymous rows
//     (account_id IS NULL, e.g. background activity) render without
//     an AccountID field on the wire.
//   - AccountEmail is the verbatim email captured at copy-time inside
//     PgStore.DeleteAccount / MemStore.DeleteAccount. It is omitempty:
//     anonymous rows have no email, and the regulator can still read
//     them with an empty AccountEmail field. Empty string == "no
//     customer-context at the moment of emission".
//
// Data is the raw jsonb payload; kind-specific schemas are documented
// per-kind in the ADR. ReceivedAt is RFC 3339 so the dashboard's
// "newest first" sort stays correct across timezones.
type AuditLogEntry struct {
	ID           string          `json:"id"` // uuid canonical form
	Kind         string          `json:"kind"`
	AccountID    string          `json:"account_id,omitempty"`    // uuid canonical; absent on anonymous rows
	AccountEmail string          `json:"account_email,omitempty"` // captured at copy-time
	Actor        string          `json:"actor,omitempty"`
	ReceivedAt   string          `json:"received_at"` // RFC 3339
	Data         json.RawMessage `json:"data,omitempty"`
}

// ListAuditLogResponse is the wire shape for GET /v1/audit-log and
// GET /v1/audit-log/all. Entries are newest-first
// (received_at DESC, id DESC) per the audit_log_received_at_idx so the
// dashboard can render top-of-list without re-sorting. Limit echoes
// the effective limit applied by the handler (capped at
// listAuditLogLimitMax) so the SDK can display "showing 50 of N"
// without re-issuing the request.
type ListAuditLogResponse struct {
	Entries []AuditLogEntry `json:"entries"`
	Limit   int             `json:"limit"`
}

// --- GitHub install bind picker (PR-B; §11) ---------------------------------
//
// InstallBindRequest is the body for both POST /v1/install/repos/list
// and POST /v1/apps/{slug}/install/bind. ProductionBranch is
// optional — when omitted, githubd uses the install's default_branch
// from /installations/{id}.
//
// RepoFullName matches GitHub's owner/name shape (e.g. "octocat/
// hello-world"). The pattern is enforced server-side in handlers_install_github.go
// but kept loose here so the SDK can serialise any GitHub-shaped
// string the dashboard holds.
type InstallBindRequest struct {
	InstallationID   int64  `json:"installation_id"`
	RepoFullName     string `json:"repo_full_name"`
	ProductionBranch string `json:"production_branch,omitempty"`
}

// InstallBindResponse is the body the dashboard parses after a
// successful bind. BindingID is the deterministic
// "bind-<appID>-<repo>" form RealService.BindAppRepo emits; audit
// log entries reference it directly.
type InstallBindResponse struct {
	BindingID        string `json:"binding_id"`
	RepoFullName     string `json:"repo_full_name"`
	ProductionBranch string `json:"production_branch"`
}

// RepoResponse is one repo visible to the user's GitHub App
// installation, as returned by githubd's
// /user/installations/{id}/repositories. Carries only the fields the
// dashboard bind picker renders; no nested owner object (the
// install URL already disambiguates).
type RepoResponse struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// AppMetricsResponse is the per-app metrics payload returned by
// GET /v1/apps/{slug}/metrics?range= (issue #273 / ADR-042).
//
// Time-windowed via the `range` query param (closed vocabulary, see
// server handler). When the underlying Prometheus client is
// unavailable, every numeric field is zero and Source is prefixed
// with "degraded: <reason>" — same contract as the public status
// page so the dashboard has one empty-state path.
//
// All percentage fields are clamped to [0, 100]; all latency
// fields are milliseconds ≥ 0. NaN/Inf come back as zero — see
// the server handler for the guard order.
type AppMetricsResponse struct {
	AppID  string `json:"app_id"`
	Range  string `json:"range"`  // echoed window, e.g. "5m"
	Source string `json:"source"` // "prometheus" on success, "degraded: <err>" otherwise
	AsOf   string `json:"as_of"`  // RFC3339Nano UTC
	// RequestCount is the count of gateway_requests_total{app} over the
	// window. Drives the empty-state message: 0 means "no requests in
	// the last 5m" rather than a row of zeros.
	RequestCount int64 `json:"request_count"`
	// LatencyP50MS / P95MS / P99MS are histogram_quantile(q) over
	// the 2xx class only — failures surface separately as
	// ErrorRatePct. NaN from histogram_quantile on an empty window
	// is coerced to 0 by the handler.
	LatencyP50MS float64 `json:"latency_p50_ms"`
	LatencyP95MS float64 `json:"latency_p95_ms"`
	LatencyP99MS float64 `json:"latency_p99_ms"`
	// ErrorRatePct is the share of [45]xx requests in the window.
	ErrorRatePct float64 `json:"error_rate_pct"`
	// ColdStartPct is the share of requests that triggered a cold
	// boot (the WakeGate leader — see ADR-042 §cold semantics).
	// Followers waiting on the gate show as zero cold contribution
	// but their wait is visible via gateway_wake_queue_wait_seconds
	// on the §12 dashboard.
	ColdStartPct float64 `json:"cold_start_pct"`
	// WakeP95MS is the FLEET wake p95 (gateway_wake_latency_seconds
	// is unlabeled — there is no per-app wake histogram). Labelled
	// as such in the UI; here it's named plainly because the
	// dashboard copy does the labelling.
	WakeP95MS float64 `json:"wake_p95_ms"`
	// QueueDepth (issue #1233 / ADR-123) is the current waiters
	// in the per-app wake gate queue, sourced from
	// gateway_queue_depth{app}. Bounded by per-plan MaxQueueDepth
	// (Hobby 5 / Pro 25 / Scale 100). The alert preset
	// queue_backlog_growing uses this via the evaluator's
	// queue_depth metric branch; the public metrics endpoint
	// surfaces it for dashboard parity.
	QueueDepth int64 `json:"queue_depth"`
	// EgressBytes (ADR-046, step 10) is the total
	// per-app egress byte delta over the window,
	// queried from vmmd_egress_net_tx_bytes_total{app}
	// (the Prometheus mirror of usage_minutes.net_tx_bytes;
	// the gateway-side tx_bytes mirror lands in PR-2).
	// Informational only — not billed. 0 when Prometheus
	// is degraded or the metric hasn't been emitted yet.
	// Unit: interface bytes (includes framing). The
	// future egress-billing PR picks the unit; this field
	// reports the Prometheus counter verbatim.
	EgressBytes int64 `json:"egress_bytes"`
	// TxBytes (ADR-046 PR-2 / issue #415 PR-2) is the
	// gateway-side mirror of EgressBytes. Source:
	// gateway_egress_tx_bytes_total{app} (the byte
	// counter drained from the per-instance ring by
	// pkg/gateway/egressgrpc/server.go; the
	// gateway-side stream consumer in
	// cmd/gatewayd-internal/egress_grpc.go feeds the
	// counter). Same window semantics as EgressBytes;
	// the dashboard surfaces both columns so operators
	// can see gateway-side vs schedd-side independently
	// (a divergence indicates the EgressSink ring is
	// dropping bytes or the gRPC stream is wedged).
	// 0 when Prometheus is degraded or the gateway
	// hasn't drained yet. Unit: interface bytes
	// (includes framing).
	TxBytes int64 `json:"tx_bytes"`
	// Routes (ADR-093) is the per-route breakdown for opt-in apps
	// (apps.route_metrics_enabled=true). nil when the app is not
	// opt-in — the dashboard distinguishes "feature off" (Routes
	// absent) from "feature on, no traffic" (Routes = []). Each
	// row is the bounded detail from the gatewayd-internal
	// in-memory reader: max 50 distinct routes + the
	// __route_other__ overflow bucket. The route label is
	// method + raw path (pre-rewrite, ADR-093 D6). The shape
	// matches the existing field-level `x-since: "2026-08"`
	// header convention so the SDK generator picks it up
	// automatically.
	Routes []RouteRow `json:"routes,omitempty"`

	// Wakes24h is the count of wake.boot_started events the
	// schedd recorded for this app in the trailing 24 hours.
	// Sourced from the events table (count over
	// kind='wake.boot_started' AND app_id=$1 AND
	// at >= now() - interval '24 hours'). The (data->>'app_id')
	// predicate is NOT covered by the existing events_wake_id_idx
	// jsonb expression index (migration 00114 indexes
	// data->>'wake_id'); on a Scale-tier app with a large fleet
	// the underlying query can seq-scan + jsonb-cast per row.
	// Best-effort: 0 when Prometheus is degraded, the events
	// row hasn't been written, or the store query fails. The
	// customer-facing dashboard surfaces this as the "wakes
	// today" line item; combined with ColdStartPct it answers
	// "is my app wake-bound or sleep-bound". The pre-ADR-123
	// fleet renders this as 0 because pre-PR-A boot_started
	// rows carry no app_id field — same posture as the
	// wake-timeline view's `WakeCountWithMeta` denominator at
	// cmd/apid/handlers_dashboard.go:2659.
	Wakes24h int64 `json:"wakes_24h,omitempty"`

	// CacheHitRatePct is the share of cache-eligible requests
	// served from gateway_response_cache (ADR-122) over the
	// window. Field is ALWAYS present on the wire so the SDK
	// can rely on the documented schema — 0 means either
	// "feature off" (no cache rule attached) or "feature on,
	// zero traffic". The dashboard distinguishes the two via
	// the existence of the `Routes` block, not via field
	// absence. Mirrors the response-cache wakes-avoided count
	// in `pkg/appmetrics.Fetch` for the operator-side view;
	// the customer-facing denominator is "requests that hit a
	// cache-eligible route" rather than the fleet-wide total
	// so a customer with a single cacheable path sees a
	// meaningful percentage rather than 0.0001%.
	//
	// Implementation note: the PromQL query against
	// gateway_response_cache_total{app_id, outcome=hit/miss}
	// is out of scope for this PR; the field stays 0 until
	// the response-cache consumer-facing metric lands.
	CacheHitRatePct float64 `json:"cache_hit_rate_pct"`

	// ErrorBudgetPct is the remaining API-availability error
	// budget as a percentage (0 = exhausted, 100 = full).
	// Field is ALWAYS present on the wire so the SDK can rely
	// on the documented schema — 0 renders as "—" on the
	// dashboard rather than a misleading "budget exhausted"
	// message. Window is the trailing 30 days (the §12 SLO
	// evaluation period). Computed as
	// `100 - (observed_error_rate_pct × 30d_window_factor)`
	// against the plan's API-availability SLO target
	// (99.5% per spec §12). Best-effort: 0 when the
	// `apid_request_total{account_id, route, code}` series is
	// degraded or the per-plan SLO target is unknown.
	//
	// Implementation note: the per-plan SLO target is not
	// yet exposed on the Limits struct (issue TBD); the
	// field stays 0 until that lands.
	ErrorBudgetPct float64 `json:"error_budget_pct"`
}

// RouteRow is the per-route detail row returned by the
// gatewayd-internal control-listener reader at
// GET /v1/internal/apps/{slug}/routes (ADR-093). The same
// structure is wrapped in AppMetricsResponse.Routes by the apid
// handler. `Route` is the label exactly as emitted on the
// Prometheus side (method + raw path, or __route_other__ for the
// overflow bucket). All latency fields are milliseconds ≥ 0;
// NaN/Inf are coerced to 0 by the reader — same contract as the
// AppMetricsResponse histograms.
type RouteRow struct {
	// Route is the bounded label: "GET /users/4f8a" for an
	// admitted route, or "__route_other__" for the overflow
	// bucket. The two reserved labels ("" and "__route_other__")
	// are surfaced verbatim so dashboards can render the
	// wildcard-route signal honestly.
	Route string `json:"route"`
	// Count is the number of requests observed in the window.
	Count uint64 `json:"count"`
	// P50MS / P95MS / P99MS are histogram_quantile over the
	// full request duration (status-agnostic — failures are
	// included, not excluded, so the percentile is the
	// latency-percentile the customer actually experiences).
	// The histogram is gateway_request_duration_seconds{app,
	// route, class} (ADR-093 D4), summed across all classes
	// for the row.
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	// ErrorPct is the share of requests with status ≥ 400 in
	// the window. Computed from
	// gateway_request_failures_total{app, route, code} /
	// gateway_requests_total{app, route}, code ≥ 400 summed.
	ErrorPct float64 `json:"error_pct"`
}

// AppRoutesResponse is the per-route label snapshot returned by
// GET /v1/apps/{slug}/routes (ADR-093). The shape is intentionally
// narrower than AppMetricsResponse — only the bounded label set
// the gatewayd-internal control listener emits (method + raw
// path, capped at 50 + __route_other__). The Prometheus-derived
// per-route rollup (count, percentiles, error_pct) lives on
// AppMetricsResponse.Routes, computed lazily when the dashboard
// needs it. Splitting the two surfaces keeps the lightweight
// reader cheap (one in-memory map read on gatewayd-internal, one
// HTTP round-trip from apid) and lets the dashboard render the
// "what routes is this app serving?" panel without a Prometheus
// query.
//
// Source is "live" when the gatewayd control listener responded
// 200; "unavailable" when the dial failed (gatewayd not
// reachable, X-Faas-Routes-State: unavailable header). Routes
// is []string (not nil) on the unavailable path so the JSON
// encoder emits `[]` rather than `null`.
//
// CapHit (ADR-093 Tier B item #1, issue #273 follow-up) is true
// iff the app's routeLabelSet has reached RouteMetricsPerAppCap
// (pkg/api.RouteMetricsPerAppCap = 50) and additional routes are
// collapsing into the reserved __route_other__ bucket. On the
// "live" path the dashboard renders CapHit=true as a "you have
// hit the 50-route cap" chip rather than counting Routes and
// trying to disambiguate "5 real routes + __route_other__
// because of one wildcard probe" from "50 real routes +
// overflow". On the "unavailable" path CapHit is the zero
// value (false) — the upstream decode doesn't carry it, and
// the dashboard already renders unavailable as a distinct chip.
type AppRoutesResponse struct {
	Slug   string   `json:"slug"`
	AppID  string   `json:"app_id,omitempty"`
	Routes []string `json:"routes"`
	Source string   `json:"source"`
	// CapHit mirrors gatewayd-internal's routesResponseJSON.CapHit.
	// ADR-093 §D2 invariant: when CapHit==true, len(Routes) ==
	// RouteMetricsPerAppCap + 2 (the +2 is reservedRouteLabelEmpty
	// + __route_other__).
	CapHit bool `json:"cap_hit"`
}

// AppStreamingStatus is the per-request streaming classification
// returned by GET /v1/apps/{slug}/streaming-cap (ADR-102 D6). It is
// the wire-level mirror of pkg/gateway.(*Handler).decideStreaming —
// a customer hitting this endpoint sees exactly what the gateway's
// gate machine resolved for the next inbound request, with the same
// status enum and the same effective cap.
//
// Status is one of the api.StreamingStatus* constants. CapKind
// labels the cap source: "plan" means app.Plan.MaxResponseBodyBytes
// (the buffered cap; for non-streaming statuses this is also the
// streaming cap because no edge rule matched), "endpoint-rule"
// means a kind=limit edge rule with a non-zero MaxBodyBytesStreaming
// field matched and overrode the plan cap. CapKind is omitted from
// the wire when there is no override so a customer whose plan cap
// applied sees a clean three-field response.
//
// PlanAllowed + FlagEnabled mirror the two booleans that gated the
// decision, so a customer can self-diagnose without a separate
// GET /v1/apps/{slug} round-trip.
type AppStreamingStatus struct {
	AppID        string          `json:"app_id"`
	Status       StreamingStatus `json:"status"`
	EffectiveCap int64           `json:"effective_cap_bytes"`
	PlanCap      int64           `json:"plan_cap_bytes"`
	FlagEnabled  bool            `json:"flag_enabled"`
	PlanAllowed  bool            `json:"plan_allowed"`
	CapKind      string          `json:"cap_kind,omitempty"`
}

// --- Account-scoped metrics rollup (issue #393) --------------------------

// AppsMetricsResponse is the rollup for GET /v1/apps/metrics?range=
// (issue #393) — one call replacing N per-app fan-outs. The wire
// shape mirrors AppMetricsResponse at the row level (each value is
// an AppMetricsResponse) so the SDK can reuse the per-app type for
// row decoding.
//
// Apps is keyed by app_slug so the dashboard can render the rows
// without a parallel /v1/apps lookup. Apps is nil (not {}) when the
// Prometheus client is unavailable — the Source field carries the
// "degraded: <reason>" contract from the per-app handler, so the
// dashboard has one empty-state branch across both endpoints.
//
// Range / Source / AsOf follow the per-app shape exactly. The
// per-app WakeP95MS is the FLEET p95 (gateway_wake_latency_seconds
// is unlabeled) — same here.
type AppsMetricsResponse struct {
	Range  string                        `json:"range"`
	Source string                        `json:"source"`
	AsOf   string                        `json:"as_of"`
	Apps   map[string]AppMetricsResponse `json:"apps"`
}

// --- Customer-facing SLO surface (issue #696 / ADR-082) -----------------

// SLODuration is the shared latency sub-shape used by AppSLOResponse
// and AccountSLOResponse. Three percentiles over the SLO window (2xx
// class only); NaN/Inf from histogram_quantile on an empty window is
// coerced to 0 by the handler (mirrors pkg/appmetrics.SafeFloat).
type SLODuration struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

// SLODefaultWindow is the server's default SLO window when the caller
// omits ?window=. Matches the issue's "default SLO window" framing —
// 24h is the canonical "yesterday's SLO" lookback.
const SLODefaultWindow = "24h"

// sloWindowSet is the closed vocabulary for the SLO `window` query
// param. Strict subset of pkg/appmetrics.Ranges() (which is the
// 7-range /metrics vocabulary) — the /slo endpoints offer only
// customer-facing SLO windows, not the 5m/15m/6h/15d "current slice"
// panels. Everything else is rejected with 400 CodeValidation.
var sloWindowSet = []string{"1h", "24h", "7d"}

// SLORanges returns a copy of the closed SLO window vocabulary. The
// copy mirrors pkg/appmetrics.Ranges()'s "callers can't mutate the
// package state" pattern.
func SLORanges() []string {
	out := make([]string, len(sloWindowSet))
	copy(out, sloWindowSet)
	return out
}

// IsValidSLORange returns true iff rng is in the closed SLO window
// set. The HTTP handler validates ?window= via this helper so the
// SDK and the CLI share the same vocabulary.
func IsValidSLORange(rng string) bool {
	for _, r := range sloWindowSet {
		if r == rng {
			return true
		}
	}
	return false
}

// AppSLOResponse is the per-app SLO panel returned by
// GET /v1/apps/{slug}/slo?window= (issue #696 / ADR-082). Distinct
// from AppMetricsResponse (issue #273): the SLO surface is a
// fixed-window (1h/24h/7d) summary of the customer-facing SLO
// signals, not a 5m slice for the dashboard. The fields overlap
// only on latency percentiles, error rate, and cold-boot rate — the
// remaining fields (wake_queue_p95, throttled_total, instance_hours,
// gb_hours) are net-new per the issue.
//
// Source follows the "degraded: <reason>" contract from
// pkg/appmetrics (so the dashboard and the SDK share one empty-state
// branch across /metrics and /slo). When Prometheus is unreachable,
// every numeric field is zero and Source is prefixed with
// "degraded: <reason>". When Postgres usage-rollup fails but the
// PromQL pass succeeded, only InstanceHours/GBHours are zeroed and
// Source is "degraded: postgres unavailable" — the latency/error/
// cold-boot numbers stay non-zero.
//
// All numeric fields are zero-on-missing (no *float64 pointers, no
// omitempty). The dashboard's "no data" branch renders on
// RequestsTotal == 0 AND InstanceHours == 0 over the longest window.
type AppSLOResponse struct {
	AppID           string      `json:"app_id"`
	AppSlug         string      `json:"app_slug"`
	Window          string      `json:"window"` // echoed window, e.g. "24h"
	Source          string      `json:"source"` // "prometheus" on success, "degraded: <reason>" otherwise
	AsOf            string      `json:"as_of"`  // RFC3339Nano UTC
	RequestDuration SLODuration `json:"request_duration"`
	ErrorRatePct    float64     `json:"error_rate_pct"`
	ColdBootRatePct float64     `json:"cold_boot_rate_pct"`
	InstanceHours   float64     `json:"instance_hours"`
	GBHours         float64     `json:"gb_hours"`
	// WakeQueueP95MS is the FLEET wake-queue p95
	// (gateway_wake_queue_wait_seconds is unlabeled — same as
	// gateway_wake_latency_seconds on the /metrics surfaces).
	// Labelled as such in the UI.
	WakeQueueP95MS float64 `json:"wake_queue_p95_ms"`
	RequestsTotal  int64   `json:"requests_total"`
	ThrottledTotal int64   `json:"throttled_total"`
}

// AppWakeTimelineResponse is the JSON mirror of the per-app
// wake-timeline dashboard page (cmd/apid/handlers_dashboard.go:2548).
// The dashboard HTML page keeps its pre-rendered HTML chips
// (RenderTriggerHistogram, RenderWakeTimelineTable); this DTO is the
// wire-friendly mirror a separate frontend agent can consume.
//
// The aggregation math (24h cutoff descending-break, two-denominator
// rule for at-capacity %, em-dash policy) is shared with the HTML
// handler via cmd/apid/handlers_wake_timeline.go::buildWakeTimeline
// — see that helper for the load-bearing invariant.
//
// Plan gate: Hobby+ (PerAppMetricsAllowed). 402 on Free — same code
// as /v1/apps/{slug}/metrics (plan_per_app_metrics_not_allowed). The
// shared accessor means a downgrade between the two endpoints
// flips both at once.
//
// Source conventions:
//   - WakeCount24h: number of instances in the trailing 24h window
//     (descending-cutoff break: the SQL is LIMIT 50, so any sane
//     customer workload lands inside the 24h window — the dashboard
//     never claims "true" 24h since it would need a separate SQL
//     scan; mirrors the HTML page's documented 50-row cap).
//   - WakeCountWithMeta: denominator for AtCapacityPct — the count
//     of those rows where the events.wake.boot_started LEFT JOIN
//     succeeded (pre-ADR-123 fleet rows contribute zero to this
//     numerator but still appear in Rows so the per-row audit
//     trail isn't lossy).
//   - AtCapacityCount / AtCapacityPct: only meaningful when
//     WakeCountWithMeta > 0; 0 otherwise (SafePercent math).
//   - TriggerHistogram: empty map (NOT null) on a fresh app, or a
//     trigger → N count of WakeBootMeta.Trigger values across the
//     meta-bearing rows.
//   - Rows: every instance in the 50-row slice whose StartedAt is
//     within the 24h cutoff, in DESC order. Pre-ADR-123 fleet rows
//     appear with the Trigger/QueuedCount/ReadyInMS fields absent
//     (zero-valued); the dashboard renders em-dash on those — the
//     JSON wire shape uses omitempty so the dashboard SPA can
//     distinguish "absent" (jsonb key missing) from "explicit zero"
//     (jsonb key present and 0).
//
// Wire freshness: AsOf is the handler's local time.Now().UTC() at
// JSON marshal; SQL reads against the events + instances tables
// happen just before, so consumers should treat AsOf as the
// authoritative "as of" instant for the row set.
type AppWakeTimelineResponse struct {
	App               WakeTimelineApp       `json:"app"`
	WakeCount24h      int                   `json:"wake_count_24h"`
	WakeCountWithMeta int                   `json:"wake_count_with_meta"`
	AtCapacityCount   int                   `json:"at_capacity_count"`
	AtCapacityPct     float64               `json:"at_capacity_pct"`
	TriggerHistogram  map[string]int        `json:"trigger_histogram"` // empty map, not nil
	Rows              []WakeTimelineJSONRow `json:"rows"`
	AsOf              string                `json:"as_of"` // RFC3339Nano UTC
}

// WakeTimelineApp is the slim per-app DTO embedded inside
// AppWakeTimelineResponse.App. The dashboard's pkg/dashboard
// AppListItem carries template-specific glyph/badge fields (SLO
// badge, StateBadge*, QuotaLabel) — those don't belong on the wire.
// The dashboard SPA only needs the bare identification (slug +
// app_id) plus a couple of status fields for the heading.
//
// Field choices are deliberate:
//   - AppID: needed for client-side cache keys + dashboard "open in
//     gateway" links; same UUID the per-app dashboard header uses.
//   - Slug: the human-readable ID the dashboard already keys by.
//   - Status: optional dashboard-rendered badge (e.g. "active" /
//     "paused"). "" when the apps row has no current deployment.
//   - URL: optional public URL once a deployment is bound; "" until
//     then. Matches the dashboard's apps-list cell.
type WakeTimelineApp struct {
	AppID  string `json:"app_id"`
	Slug   string `json:"slug"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
}

// AppUsageSummaryResponse is the wire shape for
// GET /v1/apps/{slug}/usage — per-app billing summary over a
// caller-supplied window (default: trailing 30d). Plan-gated
// Hobby+ (AppUsageSummaryAllowed). Free falls through with
// plan_app_usage_summary_not_allowed.
//
// Field-by-field:
//   - PeriodStart / PeriodEnd: half-open [PeriodStart, PeriodEnd)
//     window in UTC. Snapped to UTC midnight on the inclusive end
//     (the handler clamps; this struct just records what was
//     rolled up).
//   - MBSeconds + GBHours: rollup of usage_minutes.mb_seconds for
//     this app in the window. GBHours is the rounded float
//     (6-decimal precision — mirrors MonthlyUsageGB's rounding).
//   - Requests / TxBytes: cumulative HTTP activity (informational).
//   - BuilderSeconds: cumulative builder-microVM CPU-time
//     (informational — surfaced as a sidebar line on the dashboard).
//   - ColdBootCount: WAKE_RESTORE→WAKE_COLD_BOOT transitions.
//   - PlanIncludedGBHours: echoed from acct.Plan.PlanIncludedGBHours()
//     so the dashboard can render the included-band badge without
//     a second round-trip.
//   - OverageGBHours: max(0, gb_hours - plan_included). 0 when
//     gb_hours ≤ plan_included. The dashboard renders this as the
//     red overage chip; the Stripe pusher bills it at €0.01/GB-h.
//   - Source: "usage_minutes" today (after the 30d retention cap).
//     "usage_daily" / "mixed" land with the trail-period reader
//     follow-up — same wire shape, no migration needed.
//   - AsOf: RFC3339Nano UTC stamping the envelope's authoritative
//     "as of" instant.
type AppUsageSummaryResponse struct {
	Slug                string    `json:"slug"`
	PeriodStart         time.Time `json:"period_start"`
	PeriodEnd           time.Time `json:"period_end"`
	MBSeconds           int64     `json:"mb_seconds"`
	GBHours             float64   `json:"gb_hours"`
	Requests            int64     `json:"requests"`
	TxBytes             int64     `json:"tx_bytes"`
	BuilderSeconds      float64   `json:"builder_seconds"`
	ColdBootCount       int64     `json:"cold_boot_count"`
	PlanIncludedGBHours float64   `json:"plan_included_gb_hours"`
	OverageGBHours      float64   `json:"overage_gb_hours"`
	Source              string    `json:"source"`
	AsOf                string    `json:"as_of"`
}

// WakeTimelineJSONRow is one row of AppWakeTimelineResponse.Rows.
// Mirrors pkg/dashboard/views.WakeTimelineRow's fields so the JSON
// mirror can render the same dashboard page 1:1 — the only
// difference is the omitempty discipline on the nullable fields
// (jsonb-absent vs jsonb-present-and-zero) and the explicit
// AtCapacityPresent bit, which the HTML renderer emits as em-dash
// when false.
//
// ReadyInMS = -1 sentinel encodes "still booting or rejected, no
// boot_completed row to compute from" — the JSON wire shape picks
// -1 over the HTML page's em-dash convention because Go's json
// package can't render "—" inline. The dashboard SPA renders "—"
// on -1, mirroring the HTML page cell-empty branch
// (pkg/dashboard/views/wake_timeline.go:158).
type WakeTimelineJSONRow struct {
	Kind               string `json:"kind"`
	State              string `json:"state"`
	At                 string `json:"at"` // RFC3339
	Trigger            string `json:"trigger,omitempty"`
	QueuedCount        int32  `json:"queued_count,omitempty"`
	ConcurrencyAtAdmit int32  `json:"concurrency_at_admit,omitempty"`
	AtCapacity         bool   `json:"at_capacity"`
	AtCapacityPresent  bool   `json:"at_capacity_present"`
	ReadyInMS          int32  `json:"ready_in_ms,omitempty"` // -1 = em-dash on absent
}

// AccountSLOResponse is the flat account-wide SLO rollup returned by
// GET /v1/account/slo?window= (issue #696 / ADR-082). Mirrors the
// per-app DTO field-for-field except for AppID/AppSlug (the rollup
// is account-wide). The fields are scalar sums/rates across the
// account; per-app drill-down is served by the existing
// /v1/apps/metrics endpoint.
//
// Source / Window / AsOf follow the per-app shape exactly. The
// "degraded:" contract is identical: Prometheus-down → zeroed
// fields with the reason in Source; Postgres hiccup → only
// InstanceHours/GBHours zeroed with "degraded: postgres unavailable"
// in Source.
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

// ProjectScanRequest is the multipart body for POST /v1/projects/scan.
// Defined as a DTO (rather than an inline handler struct) so the
// schema-parity AST gate can assert field-for-field equivalence with
// the OpenAPI spec.
type ProjectScanRequest struct {
	Source           string `json:"source"`            // tar.gz binary blob
	ProjectSlug      string `json:"project_slug"`      // kebab slug
	ProductionBranch string `json:"production_branch"` // default "main"
	InstallID        int64  `json:"install_id"`        // GitHub install id (--repo); 0 for unbound
	Only             string `json:"only"`              // CSV of workload names
}

// ProjectApplyRequest is the multipart body for POST /v1/projects.
// Shape mirrors ProjectScanRequest — the handler re-runs the scan
// and re-checks the plan token internally.
type ProjectApplyRequest struct {
	Source           string `json:"source"`
	ProjectSlug      string `json:"project_slug"`
	ProductionBranch string `json:"production_branch"`
	InstallID        int64  `json:"install_id"`
	Only             string `json:"only"`
}

// SourceRefDeployRequest is the JSON body for
// POST /v1/apps/{slug}/deployments/source-ref (DEPLOY-PROV-4 /
// ADR-092, issue #739). The CLI never sees the GitHub install
// token; apid resolves it server-side from the durable
// github_installations row (state.GitHubInstallForAccount),
// then dials codeload.github.com via the githubd bridge
// (MintInstallationToken + StreamSourceRef gRPCs).
//
// Ref accepts a 40-char commit SHA, a 7+ char short SHA, a
// branch name, or a tag. Branches/tags are normalised to a SHA
// server-side via api.github.com/repos/<repo>/commits/<ref>
// before the fetch (the audit row's data.source_sha carries
// the resolved SHA — not the customer's input). Format is
// reserved for future wire shapes (zipball, git bundle); v1
// only ships "tarball". Empty Format defaults to "tarball".
type SourceRefDeployRequest struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Format string `json:"format,omitempty"`
	// Annotation fields (issue #977 / ADR-116). The GitHub
	// Action .github/actions/deploy passes these from the
	// action.yml inputs (reason / tag / deployed-by / pr-number);
	// deployed-by defaults to ${{ github.actor }} and pr-number
	// defaults to ${{ github.event.pull_request.number }} on the
	// Action side. All four are optional; the apid handler stamps
	// them onto the deployment row + the audit data{} payload.
	Reason     string `json:"reason,omitempty"`
	Tag        string `json:"tag,omitempty"`
	DeployedBy string `json:"deployed_by,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
}

// SourceTarballDeployRequest is the CLI-uploaded tarball sidecar for
// POST /v1/apps/{slug}/deployments/source-tarball (issue #961 / Mega-A
// PR-1). Repo + Ref are optional, informational, and recorded on the
// build row verbatim; the build pipeline does NOT use them to fetch
// upstream. The tarball itself is uploaded as the multipart `tarball`
// field. See docs/adr/0XX-local-tarball-deploy-trust-root.md.
type SourceTarballDeployRequest struct {
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Annotation fields (issue #977 / ADR-116). All four are
	// optional; the CLI's zero-config path auto-captures
	// DeployedBy from `git config user.name` when in a repo (see
	// cmd/gregale/cmd_deploy_zero_config.go). Reason and Tag
	// come from --reason / --tag; PRNumber is not normally
	// supplied on a tarball deploy (it would be inferred from
	// a paired GitHub Action, not the tarball CLI).
	Reason     string `json:"reason,omitempty"`
	Tag        string `json:"tag,omitempty"`
	DeployedBy string `json:"deployed_by,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
}

// PlanWorkload mirrors reposcan.Workload (Phase 3 wire shape).
// Field names match the OpenAPI schema verbatim — the spec-check
// AST gate enforces the field-for-field mapping.
//
// Action + ExistingAppID are the ADR-124 blast-radius projections:
// they tell the client whether the workload will create or update an
// existing app, and which existing app row the update targets. ID is
// empty when Action == "create".
type PlanWorkload struct {
	Name          string   `json:"name"`
	RootDir       string   `json:"root_dir"`
	Dockerfile    string   `json:"dockerfile,omitempty"`
	Command       []string `json:"command"`
	Class         string   `json:"class,omitempty"`
	Schedule      string   `json:"schedule,omitempty"`
	Ports         []int    `json:"ports"`
	EnvKeys       []string `json:"env_keys,omitempty"`
	Source        string   `json:"source,omitempty"`
	Tier          string   `json:"tier,omitempty"`
	Action        string   `json:"action,omitempty"`          // ADR-124: "create" | "update"
	ExistingAppID string   `json:"existing_app_id,omitempty"` // ADR-124: empty iff Action == "create"
}

// PlanManaged mirrors reposcan.Managed.
type PlanManaged struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	EnvHint string `json:"env_hint"`
	Source  string `json:"source"`
	Image   string `json:"image"`
}

// PlanCron is the per-cron line in the scan response. Carries the
// workload name (NOT the AppID — that's resolved at apply time).
type PlanCron struct {
	WorkloadName string `json:"workload_name"`
	Schedule     string `json:"schedule"`
	Path         string `json:"path"`
	Enabled      bool   `json:"enabled"`
}

// PlanAffectedApp is one row of the ADR-124 affected-workloads
// partition (PlanResponse.WillDeploy / Unaffected). It pairs an
// existing-or-future app with a closed-vocabulary Action that tells
// the operator what the scan will do to it. ID is empty for Action
// == "create" (the row has no persisted app yet); ExistingRootDir is
// populated only when the existing app's RootDir differs from the
// scan-time RootDir, surfacing (slug, root) collisions in monorepos.
//
// Action vocabulary (ADR-124):
//
//	"create" — scan workload, no existing app row matches (RootDir, Name).
//	"update" — scan workload, existing app matches (RootDir, Name).
//	"remove" — existing app, no scan workload with the same (RootDir, Name)
//	           and not protected by --exclude. Will trigger
//	           SoftDeleteAppCascade on the apply path.
//	"noop"   — either (a) existing app + scan workload match and the
//	           operator excluded it via --exclude, or (b) no scan change
//	           (manifest config matches app state byte-for-byte). The
//	           apply path leaves noop rows untouched.
type PlanAffectedApp struct {
	Slug            string `json:"slug"`
	ID              string `json:"id,omitempty"` // empty iff Action == "create"
	Action          string `json:"action"`       // "create" | "update" | "remove" | "noop"
	ExistingRootDir string `json:"existing_root_dir,omitempty"`
}

// QuotaBlock is the limit + observed extension on a plan-quota
// problem. Mirrors api.Problem.WithLimit — emitted alongside any
// 402/403 quota response so the CLI can render "X/Y apps" without
// a second request.
type QuotaBlock struct {
	Limit    int64  `json:"limit,omitempty"`
	Observed int64  `json:"observed,omitempty"`
	DocsURL  string `json:"docs_url,omitempty"`
}

// PlanResponse is the dry-run response from POST /v1/projects/scan.
// Fields mirror scanPlanResponse in cmd/apid/scan_service.go; the
// DTO is the wire shape, the in-process struct is the
// handler-internal carrier.
//
// WillDeploy, Unaffected, Skipped, and Removed are the ADR-124
// blast-radius projection. They enumerate every existing app in the
// account (Unaffected = noop or skipped) plus the scan's proposed
// creates (WillDeploy.Action == "create"). Removed is a flat slug
// list — removal has no per-row editable metadata worth surfacing.
type PlanResponse struct {
	ProjectSlug     string         `json:"project_slug"`
	RepoFullName    string         `json:"repo_full_name,omitempty"`
	ScanSource      string         `json:"scan_source"`
	Tier            string         `json:"tier"`
	Workloads       []PlanWorkload `json:"workloads"`
	Managed         []PlanManaged  `json:"managed"`
	Crons           []PlanCron     `json:"crons"`
	Warnings        []string       `json:"warnings,omitempty"`
	ObservedApps    int            `json:"observed_apps"`
	ObservedCrons   int            `json:"observed_crons"`
	LimitApps       int            `json:"limit_apps"`
	LimitCrons      int            `json:"limit_crons"`
	CanApply        bool           `json:"can_apply"`
	CronsNotAllowed bool           `json:"crons_not_allowed,omitempty"`
	PlanToken       string         `json:"plan_token"`
	// ADR-124 can_apply rescue signal. PreExclude is the gate
	// evaluated on the full scan (pre-`--only`/pre-`--exclude`).
	// Rescued is true when --exclude flipped a blocked gate to
	// allowed. Reasons is the human-readable failure list for
	// the post-exclude state; the dashboard renders it verbatim
	// in the gate card so the operator sees why a still-blocked
	// gate is blocked. omitempty drops these on the success
	// path so existing --json consumers stay stable.
	CanApplyPreExclude   bool     `json:"can_apply_pre_exclude,omitempty"`
	GateRescuedByExclude bool     `json:"gate_rescued_by_exclude,omitempty"`
	CanApplyReasons      []string `json:"can_apply_reasons,omitempty"`
	// ADR-124: blast-radius partition. WillDeploy + Unaffected
	// together enumerate every non-deleted app in the account plus
	// the scan's proposed creates. Skipped is the operator-excluded
	// subset of WillDeploy. Removed is the destructive subset of
	// Unaffected (existing apps absent from the scan).
	WillDeploy []PlanAffectedApp `json:"will_deploy,omitempty"`
	Unaffected []PlanAffectedApp `json:"unaffected,omitempty"`
	Skipped    []PlanAffectedApp `json:"skipped,omitempty"`
	Removed    []string          `json:"removed,omitempty"`
	// PersistedExclusions (ADR-124 follow-up #3) lists every slug
	// that was folded into this scan/apply from the persisted
	// deployment_scope_exclusions table — i.e. the operator's
	// "I excluded this for the long haul" intent. The handler
	// emits one KindProjectScopeExcluded audit row per slug. Empty
	// on the common path (no persisted exclusions); the omitempty
	// keeps existing --json consumers stable.
	PersistedExclusions []string `json:"persisted_exclusions,omitempty"`
	// StalePersistedExclusions (code-review fix #2) lists every
	// slug that was carried forward from the persisted table but
	// is no longer present in the current scan (workload was
	// renamed or deleted in a future commit). Surfaced so the
	// dashboard can render a "persisted exclusion ignored"
	// badge and so the operator can run
	// `gregale deployments exclude clear --slug=...` to drop a
	// stale row before the 90-day janitor reaps it.
	StalePersistedExclusions []string `json:"stale_persisted_exclusions,omitempty"`
}

// ApplyResponse is the success body for POST /v1/projects. Carries
// the inserted project_id + per-app IDs so the CLI's --yes flow
// can render "applied: <slug> → <app_id>".
//
// Builds carries the per-workload (deployment_id, build_id) results
// from the apply-time build-enqueue loop (PR-A, repo decomposition
// Phase 5 close-the-loop). omitempty keeps existing --json consumers
// stable; the field is only populated when the apply path actually
// enqueued builds (i.e. when at least one app was added or changed).
type ApplyResponse struct {
	PlanResponse
	ProjectID string             `json:"project_id"`
	Apps      []ApplyResponseApp `json:"apps"`
	Builds    []AppliedBuild     `json:"builds,omitempty"`
}

// ApplyResponseApp is the per-app line in the apply response.
type ApplyResponseApp struct {
	Slug string `json:"slug"`
	ID   string `json:"id"`
}

// AppliedBuild is the per-workload build result that
// cmd/apid/scanService returns alongside the reconcile Result.
// Renders as {slug, app_id, deployment_id, build_id, error?}.
// On staging or enqueue failure, Error is non-empty and the
// deployment/build IDs are empty. Partial failure is by design.
type AppliedBuild struct {
	Slug         string `json:"slug"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	BuildID      string `json:"build_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// --- cosign trusted-publisher wire types (issue #472 / ADR-054) -------------
//
// TrustedSigner is one row of the per-app cosign trusted-publisher
// list. Mirrors AWS Lambda's TrustedSigner's profileArn / profileVersionArn
// pair — the signer_name is the customer-facing label and the
// cosign_public_key_pem is the DER SPKI the verify path matches against.
//
// PublicKeyPEM is base64-encoded DER (the wire form), distinct from
// the bytea shape pkg/state.AppTrustedSigner stores (already a
// []byte). The handler decodes on the read side before reaching the
// state layer.
//
// AddedAt is RFC 3339 (Go time.Time default JSON marshal). AddedBy
// is the operator account email (NOT the account UUID), surfaced so
// the dashboard's audit-log panel can show "rotated by alice@…" without
// a second round-trip to /v1/account.
type TrustedSigner struct {
	Name         string    `json:"name"`
	PublicKeyPEM string    `json:"public_key_pem"`
	AddedAt      time.Time `json:"added_at"`
	AddedBy      string    `json:"added_by,omitempty"`
}

// AppTrustedSignerListResponse is the body of
// GET /v1/apps/{slug}/trusted_signers. Always an empty slice (never
// nil) so the JSON shape is stable — same posture as
// AppResponse.EgressAllowlist.
type AppTrustedSignerListResponse struct {
	Signers []TrustedSigner `json:"signers"`
}

// AddTrustedSignerRequest is the body of
// PUT /v1/apps/{slug}/trusted_signers/{name}. PublicKeyPEM is the
// DER SPKI as a base64 string (NOT a PEM-armoured block — the wire
// is binary-clean so we don't have to parse the PEM header to reach
// the bytes). 64..1024 bytes after decode (matches the DB CHECK
// app_trusted_signers_pem_shape); the handler validates before
// INSERT and returns 400 deploy_signature_invalid_key on shape
// failures.
type AddTrustedSignerRequest struct {
	PublicKeyPEM string `json:"public_key_pem"`
}

// AppSecurityRequest is the body of PATCH /v1/apps/{slug}/security.
// RequireSigned is a *bool so the wire form can distinguish "don't
// touch" (nil) from "explicit true/false" — the Set-bit convention
// the broader UpdateAppRequest uses (issue #471 streaming flag
// precedent). Admin-scoped surface (the customer PATCH
// /v1/apps/{slug} silently drops require_signed).
type AppSecurityRequest struct {
	RequireSigned *bool `json:"require_signed,omitempty"`
}

// AppSecurityResponse is the success body of PATCH
// /v1/apps/{slug}/security. Mirrors the AppResponse RequireSigned
// field so the CLI can render the new state without a follow-up GET.
type AppSecurityResponse struct {
	RequireSigned bool `json:"require_signed"`
}

// AppStaticEgressIPResponse is the body of GET
// /v1/apps/{slug}/static-egress-ip (ADR-119). IP / SetAt are
// pointers so the wire shape is stable: a Scale customer with no
// pin yet sees ip=null, set_at=null, plan_cap=1. PlanCap is the
// Limits.StaticEgressIPsPerApp value (1 in v1) so the dashboard
// can render "you can use 1 static IP per app" without the CLI
// round-tripping the plan table.
type AppStaticEgressIPResponse struct {
	IP          *netip.Addr `json:"ip"`
	SetAt       *time.Time  `json:"set_at"`
	PlanCap     int         `json:"plan_cap"`
	PlanAllowed bool        `json:"plan_allowed"`
}

// SetAppStaticEgressIPRequest is the body of PUT
// /v1/apps/{slug}/static-egress-ip. IP is the canonical
// customer-supplied IPv4 (dotted-quad string). The handler
// validates the family=4 + non-RFC1918 + non-link-local +
// non-multicast before the column write. Set=false means
// "clear" — the same wire body covers the DELETE /keep-IP
// promotion path without a third endpoint.
type SetAppStaticEgressIPRequest struct {
	IP  string `json:"ip"`
	Set bool   `json:"set"`
}

// AdminSetGithubWebhookSecretRequest is the body shape for
// POST /v1/admin/github-webhook-secrets (PR-D / ADR-012 §7
// amendment). Per-tenant override of the platform-wide
// FAAS_GITHUB_WEBHOOK_SECRET so a leaked tenant secret can
// rotate without coordinating every GitHub App install.
//
// InstallationID is the GitHub Apps installation_id (signed
// bigint). SecretHex is the secret in lowercase hex (16..128
// hex chars; server-side bytea-stored). The CLI takes hex so
// the plaintext never has to be a binary argv value or land in
// shell history; the apid handler hex-decodes before the
// INSERT.
//
// Auth: admin-scoped API key (ScopesAdminOnly) + email in
// FAAS_ADMIN_EMAILS allowlist (matching the credit + sign-keys
// routes). Mounted in apid under the existing
// authLimited → requireMFA → requireScope chain
// (cmd/apid/server.go).
type AdminSetGithubWebhookSecretRequest struct {
	InstallationID int64  `json:"installation_id"`
	SecretHex      string `json:"secret_hex"`
}

// AdminSetGithubWebhookSecretResponse is the row shape echoed
// back to the operator so a CI loop can confirm the rotation
// landed (UpgradedBy stamps the admin id; UpgradedAt is the
// RFC 3339 timestamp from now()). The Prometheus counter
// githubd_webhook_secret_total{status="set"} is emitted
// server-side at the apid handler.
type AdminSetGithubWebhookSecretResponse struct {
	InstallationID int64     `json:"installation_id"`
	UpgradedAt     time.Time `json:"upgraded_at"`
	UpgradedBy     string    `json:"upgraded_by"`
}

// SidecarType is the closed enum on Sidecar.Type (issue #463 /
// ADR-068 §Decision 1). The 2-sidecar cap is enforced as 1 init +
// 1 sidecar per deployment — `Sidecars.Validate` rejects any other
// shape (e.g. 2 init) with `ErrSidecarInvalidType`.
type SidecarType string

const (
	// SidecarTypeInit runs ONCE before the main workload. Exit
	// code 0 → continue, non-zero → fail the deploy with
	// `failure_class=user_error` (PR-B's runtime contract). The
	// common shape is a DB migrator: "run this image once, then
	// start the app".
	SidecarTypeInit SidecarType = "init"
	// SidecarTypeSidecar runs ALONGSIDE the main workload for
	// the lifetime of the instance. Common shape is a metrics
	// scraper exposing /metrics on the tenant netns.
	SidecarTypeSidecar SidecarType = "sidecar"
)

// EvictionPriority is the per-app tier classification (issue #475).
// 'best_effort' keeps the historical LRU-by-last_request_at reaper
// behaviour: under cross-account RAM pressure, schedd may park the
// instance to make room for someone else's bursty workload. 'reserved'
// still obeys idle / per-account / per-app caps but is protected from
// cross-account RAM-pressure eviction — every best_effort candidate
// is drained before any reserved is parked. The category is closed
// and the values are SQL CHECK (apps_eviction_priority_chk) — adding
// a third tier is a migration + ADR + handler + reaper sort change,
// not a config flag. The schema default is 'best_effort' for every
// pre-#475 row.
type EvictionPriority string

const (
	// EvictionPriorityBestEffort is the pre-#475 default tier. The
	// reaper treats this instance as a fair candidate for
	// cross-account RAM-pressure eviction; LRU-by-last_request_at
	// applies unchanged.
	EvictionPriorityBestEffort EvictionPriority = "best_effort"
	// EvictionPriorityReserved is the opt-in protected tier
	// (Plan.EvictionPriorityReservedAllowed; per-account
	// ReservedConcurrencyPerAccount cap). The reaper still reaps
	// idle / aggressive paths and the per-account RAM cap still
	// applies; this tier only protects against the cross-account
	// RAM-pressure eviction path.
	EvictionPriorityReserved EvictionPriority = "reserved"
)

// sidecarNameRe matches RFC 1123 label: lowercase alphanumeric + dash,
// 1..63 chars, starts with [a-z0-9]. Mirrors the `apps.slug` regex
// shape so the dashboard and CLI can use the same identifier
// grammar for app-sidecar and app-slug paths. Anchored.
var sidecarNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// sidecarImageRe is the digest-pinning predicate for a sidecar
// image reference. Mirrors the canonical pattern at
// cmd/apid/handlers.go:484 (digestPinnedRE) — duplicated here
// because pkg/api cannot import cmd/apid (the daemon import
// direction is one-way: cmd/apid → pkg/api). PR-B's runtime
// effect may promote this to a single shared helper in pkg/oci
// if imaged's pull path also wants the API gate inline; for
// PR-A the two-call-site duplication is acceptable.
var sidecarImageRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]+)?/[A-Za-z0-9_./-]+@sha256:[0-9a-f]{64}$`)

// Sidecar is one entry in the deploy request's `sidecars` array
// (issue #463 / ADR-068). At most one with type=init and at most
// one with type=sidecar per app (the 2-sidecar hard cap, enforced
// by `Sidecars.Validate` + the schema CHECK on
// `deployments.sidecars` in migration 00095).
//
// The env map is stored envelope-sealed at rest via
// `secretbox.SealBytes` (namespace="sidecar_env", mirrors
// `AppSecret`'s namespace="secrets"); the wire shape is plaintext
// (the apid handler seals post-decode). Plaintext values NEVER
// appear in any `slog` field, audit payload, error string, or
// HTTP response — pinned by capture-based tests in
// cmd/apid/handlers_deployments_test.go.
//
// The image reference MUST be digest-pinned (`repo@sha256:...`).
// Tag-pinning is the documented OCI supply-chain attack vector;
// the runtime image already enforces this in pkg/imaged; the
// API gate surfaces a useful error at the client side before
// the request reaches imaged.
//
// RamMB is the cgroup memory ceiling for this sidecar (PR-B
// wires the cgroup scope). 0 means "absent / inherit the app's
// plan RAM" (the common case). 32..512 enforced at the API
// layer; the "+8 MB" baseline (PerVMOverheadMB) is added once
// per instance in PR-B's admission, not per sidecar.
type Sidecar struct {
	// Name matches the RFC 1123 label grammar. Unique within a
	// single request. Required.
	Name string `json:"name"`
	// Image is the digest-pinned OCI reference (`repo@sha256:...`).
	// Tag references rejected. Required.
	Image string `json:"image"`
	// Type is the closed enum (init | sidecar). At most one of
	// each per deployment. Required.
	Type SidecarType `json:"type"`
	// Cmd is the argv array (the image's ENTRYPOINT is unchanged;
	// Cmd overrides the CMD). Every element non-empty if present.
	Cmd []string `json:"cmd,omitempty"`
	// Env is the env map. Key per `ValidateEnvKey`
	// (^[A-Z][A-Z0-9_]*$); per-value byte cap per
	// `Limits.EnvValueMaxBytes`. Values are sealed at rest via
	// secretbox. Plaintext on the wire; sealed on the column.
	Env map[string]string `json:"env,omitempty"`
	// Port is the listen port. 0 means "absent / fall back to
	// image default" (1..65535 enforced at the API layer). The
	// host-side plumbing that propagates this value to netns +
	// vmmd waitReady + runners ships in PR-B / PR-C; PR-A only
	// persists the field.
	Port int `json:"port,omitempty"`
	// RamMB is the cgroup memory ceiling for this sidecar. 0
	// means "absent / inherit the plan RAM" (the common case).
	// 32..512 enforced at the API layer.
	RamMB int `json:"ram_mb,omitempty"`
	// Essential defaults to true. If true and the sidecar exits
	// non-zero: type=init → fail the deploy with
	// `failure_class=user_error`; type=sidecar → restart-loop.
	// If false: warn-log + restart-cap (PR-B's runtime contract).
	// *bool so the wire form can distinguish "don't set" (nil)
	// from "explicit true/false". PR-A only persists the field;
	// the runtime effect is PR-B.
	Essential *bool `json:"essential,omitempty"`
}

// Validate enforces ADR-068 §Decisions 1, 2, 4, 5: name grammar,
// digest-pinning, type ∈ {init, sidecar}, cmd element non-empty,
// env key grammar + per-value byte cap, port 0/absent or 1..65535,
// ram_mb 0/inherit or 32..512, stateful denylist.
//
// Returns nil on success or a *Problem with RFC 7807 status 400
// (or 403 for stateful image). The handler maps this directly to
// api.WriteProblem; no further error wrapping needed.
func (s *Sidecar) Validate(limits Limits) *Problem {
	if s == nil {
		return nil
	}
	if !sidecarNameRe.MatchString(s.Name) {
		return ErrSidecarInvalidName(s.Name)
	}
	if !sidecarImageRe.MatchString(s.Image) {
		return ErrSidecarInvalidImage(s.Name,
			fmt.Errorf("not a digest-pinned reference (got %q)", s.Image))
	}
	// Stateful denylist gate (ADR-068 §Decision 4). The image is
	// already digest-pinned above, so the reference shape is
	// `repo@sha256:...`; the statefuldenylist matcher strips the
	// digest suffix + any registry hostname and probes every path
	// segment against the denylist set. The matcher is shared with
	// the imaged runtime gate (pkg/statefuldenylist) so the
	// apid-side 403 and the imaged pull-path rejection agree on
	// every reference shape. A 403 here pre-empts the request
	// before it ever reaches imaged — the customer sees a useful
	// error in their browser, not a pending→failed transition.
	if hint, denied := statefuldenylist.Match(s.Image); denied {
		return ErrSidecarStatefulDeniedWithHint(s.Name, s.Image, hint)
	}
	if s.Type != SidecarTypeInit && s.Type != SidecarTypeSidecar {
		return ErrSidecarInvalidType(s.Name, string(s.Type))
	}
	for i, c := range s.Cmd {
		if c == "" {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid sidecar",
				fmt.Sprintf("sidecar[%q].cmd[%d] is empty; every argv element must be non-empty.", s.Name, i))
		}
	}
	for k, v := range s.Env {
		if p := ValidateEnvKey(k); p != nil {
			return p
		}
		if limits.EnvValueMaxBytes > 0 && len(v) > limits.EnvValueMaxBytes {
			return NewProblem(http.StatusRequestEntityTooLarge,
				CodeEnvVarValueTooLarge, "Invalid sidecar env",
				fmt.Sprintf("sidecar[%q].env[%q] value is %d bytes; max is %d.",
					s.Name, k, len(v), limits.EnvValueMaxBytes)).
				WithLimit(int64(limits.EnvValueMaxBytes), int64(len(v)))
		}
	}
	if s.Port != 0 && (s.Port < 1 || s.Port > 65535) {
		return ErrSidecarInvalidPort(s.Port)
	}
	if s.RamMB != 0 && (s.RamMB < 32 || s.RamMB > 512) {
		return ErrSidecarInvalidRamMB(s.RamMB)
	}
	return nil
}

// Validate enforces the 2-cap (global `SidecarCapMax` constant),
// type-uniqueness (at most one init + one sidecar), name
// uniqueness, and per-sidecar `Validate`.
//
// The limits argument is reserved for a future per-plan
// `SidecarAllowed` gate (PR-A's accessor returns true for every
// plan; the gate is currently unused). Passing the limits keeps
// the signature forward-compatible without an ADR delta.
func (ss Sidecars) Validate(limits Limits) *Problem {
	if len(ss) == 0 {
		return nil
	}
	if len(ss) > SidecarCapMax {
		return ErrSidecarCapExceeded(len(ss), SidecarCapMax)
	}
	seen := map[SidecarType]int{}
	names := map[string]bool{}
	for i := range ss {
		if p := ss[i].Validate(limits); p != nil {
			return p
		}
		if names[ss[i].Name] {
			return NewProblem(http.StatusBadRequest, CodeValidation,
				"Invalid sidecar",
				fmt.Sprintf("sidecar name %q appears more than once.", ss[i].Name))
		}
		names[ss[i].Name] = true
		seen[ss[i].Type]++
		if seen[ss[i].Type] > 1 {
			return ErrSidecarInvalidType(ss[i].Name,
				fmt.Sprintf("at most one sidecar of type %q (got %d)", ss[i].Type, seen[ss[i].Type]))
		}
	}
	return nil
}

// --- per-account egress allowlist extra (issue #679 / PR-B / ADR-082) ---

// MaxAccountEgressAllowlistExtra is the admin-set ceiling on the
// per-account additive budget on top of the plan's
// apps.egress_allowlist cap (issue #679 / PR-B / ADR-082). Flat
// 1024 — comfortably above the largest realistic override a Pro
// or Scale account needs (Pro 16 + 1008 = 1024 max, Scale 64 +
// 960 = 1024 max). The cap is intentional: a single account's
// effective allowlist approaching 1024 entries is a customer-
// abuse signal (a misconfigured SDK would round-trip the entire
// internet into the per-app set). Operators wanting more should
// use the operator-bundle (PR-A / ADR-081) which is a separate
// additive axis that doesn't consume per-account slot.
const MaxAccountEgressAllowlistExtra = 1024

// SetAccountEgressAllowlistExtraRequest is the body of
// PATCH /v1/account/egress_allowlist_extra (issue #679 / PR-B /
// ADR-082). Extra is the per-account additive budget on top of
// the plan cap. Extra < 0 is rejected at the apid gate with
// ErrAccountEgressAllowlistExtraOutOfRange; Extra > 1024 is the
// admin-set ceiling (MaxAccountEgressAllowlistExtra). Extra ==
// 0 clears the override (the plan cap is authoritative again).
type SetAccountEgressAllowlistExtraRequest struct {
	Extra int `json:"extra"`
}

// AccountEgressAllowlistExtraResponse is the body of GET
// /v1/account/egress_allowlist_extra (issue #679 / PR-B /
// ADR-082). Extra is the per-account additive budget; PlanCap
// is the plan-only cap (Pro 16 / Scale 64 / Free,Hobby 0); the
// effective cap is PlanCap + Extra. MaxExtra is the admin-set
// ceiling (1024) so the dashboard can render the range slider
// without a second round-trip.
type AccountEgressAllowlistExtraResponse struct {
	Extra    int `json:"extra"`
	PlanCap  int `json:"plan_cap"`
	MaxExtra int `json:"max_extra"`
}

// ----------------------------------------------------------------------------
// Edge rules (ADR-089). Seven per-kind action shapes share one
// EdgeRuleResponse; the action column is jsonb in the schema and
// json.RawMessage on the wire so the SDK / Node / Python
// generators don't need to model every kind. A typed SDK that
// unmarshals into one of seven structs based on Kind is a
// follow-up ergonomic PR.
//
// Every per-kind *Action has a Validate() *Problem method so the
// apid handler can short-circuit on bad input before the store
// is touched. The validator is the canonical "closed enum + size
// bounds" shape used by PublicAuthBlock.Validate and
// CreateDeploymentOverrides.Validate.
// ----------------------------------------------------------------------------

// EdgeRuleRouteAction re-targets the request to another app owned
// by the same account.
type EdgeRuleRouteAction struct {
	TargetAppSlug string `json:"target_app_slug"`
}

func (a *EdgeRuleRouteAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("route action is required")
	}
	if a.TargetAppSlug == "" {
		return ErrValidation("route action requires target_app_slug")
	}
	if len(a.TargetAppSlug) > 40 {
		return ErrValidation(fmt.Sprintf("route action target_app_slug exceeds 40 chars (got %d)", len(a.TargetAppSlug)))
	}
	return nil
}

// EdgeRuleRewriteAction mutates the request path before forwarding.
type EdgeRuleRewriteAction struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (a *EdgeRuleRewriteAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("rewrite action is required")
	}
	if !strings.HasPrefix(a.From, "/") {
		return ErrValidation("rewrite action From must start with '/'")
	}
	if a.To == "" {
		return ErrValidation("rewrite action To is required")
	}
	return nil
}

// EdgeRuleRedirectAction is a 3xx short-circuit.
type EdgeRuleRedirectAction struct {
	StatusCode int               `json:"status_code"`
	To         string            `json:"to"`
	Headers    map[string]string `json:"headers,omitempty"`
}

func (a *EdgeRuleRedirectAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("redirect action is required")
	}
	switch a.StatusCode {
	case 301, 302, 307, 308:
	default:
		return ErrValidation(fmt.Sprintf("redirect action status_code must be one of 301,302,307,308 (got %d)", a.StatusCode))
	}
	if a.To == "" {
		return ErrValidation("redirect action To is required")
	}
	return nil
}

// EdgeRuleHeaderOp is one mutation. Action ∈ {add,set,remove}.
type EdgeRuleHeaderOp struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Action string `json:"action"`
}

// edgeRuleHeaderForbidden is the hard-coded blacklist of header
// names a kind=headers rule cannot mutate (ADR-091 §Decision).
// Per-app configurability is deferred to v2; the closed list ships
// in v1 as a wire-bypass backstop.
var edgeRuleHeaderForbidden = map[string]struct{}{
	"Host":              {},
	"Content-Length":    {},
	"Transfer-Encoding": {},
	"Connection":        {},
}

func (op *EdgeRuleHeaderOp) Validate() *Problem {
	if op == nil {
		return ErrValidation("header op is required")
	}
	if op.Name == "" {
		return ErrValidation("header op name is required")
	}
	switch op.Action {
	case "add", "set", "remove":
	default:
		return ErrValidation(fmt.Sprintf("header op action must be one of add,set,remove (got %q)", op.Action))
	}
	if _, bad := edgeRuleHeaderForbidden[op.Name]; bad {
		return ErrHeaderMutationForbidden(op.Name)
	}
	if strings.HasPrefix(strings.ToLower(op.Name), "x-faas-") {
		return ErrHeaderMutationForbidden(op.Name)
	}
	return nil
}

// EdgeRuleHeadersAction mutates request + response headers.
type EdgeRuleHeadersAction struct {
	RequestHeaders  []EdgeRuleHeaderOp `json:"request_headers,omitempty"`
	ResponseHeaders []EdgeRuleHeaderOp `json:"response_headers,omitempty"`
}

func (a *EdgeRuleHeadersAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("headers action is required")
	}
	if len(a.RequestHeaders) == 0 && len(a.ResponseHeaders) == 0 {
		return ErrValidation("headers action requires at least one request_headers or response_headers op")
	}
	for i := range a.RequestHeaders {
		if p := a.RequestHeaders[i].Validate(); p != nil {
			return p
		}
	}
	for i := range a.ResponseHeaders {
		if p := a.ResponseHeaders[i].Validate(); p != nil {
			return p
		}
	}
	return nil
}

// EdgeRuleCORSAction stamps CORS headers + handles preflight.
//
// CorsPresetID (issue #975 #4 PR-B / ADR-129 D1/D2) is the
// nullable pointer to a cors_presets row. When set, the inline
// action fields MUST be empty/zero — the preset is the entire
// policy. The compile-side merge helper
// pkg/state.MergeCorsPresetIntoRule (kind=cors branch in
// cmd/gatewayd-internal/edge_rules.go::compileCORSRules) resolves
// the preset's allow_origins / allow_methods / allow_headers /
// expose_headers / allow_credentials / max_age_seconds into the
// runtime CORS response. The apid-write boundary rejects
// `cors_preset_id + any non-empty inline field` with 422
// (Validate enforces the mutual exclusivity at create time).
type EdgeRuleCORSAction struct {
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAgeSeconds    int      `json:"max_age_seconds"`
	// CorsPresetID is *string (nullable). nil = inline policy
	// only. Non-nil = preset reference; inline fields above
	// MUST be empty/zero per ADR-129 D2.
	CorsPresetID *string `json:"cors_preset_id,omitempty"`
}

// CorsOriginPattern is the grammar for an allow_origins entry
// after the CORS improvements PR. Three forms are accepted:
//
//	"*"                                  bare full-wildcard
//	"https://app.example.com"            literal origin
//	"https://*.example.com"              subdomain wildcard (* as a
//	                                     complete left-most host label)
//	"https://localhost:*"                port wildcard (* as a complete
//	                                     port — useful for local dev)
//	"https://api.example.com:*"          host + port wildcard
//
// The grammar is deliberately tiny (no regex metacharacters, no
// path matching, no scheme matching) so the gateway hot-path
// matcher can stay an O(n) string-prefix scan without backtracking.
// The regex below enforces the grammar at create-time; the gateway
// applies the same predicates in handler.go::matchOrigin (so a
// rule that bypasses the apid validator still matches what the
// customer expects — defence in depth).
//
// Footgun guard (ADR-091 D12) only fires for the bare "*" entry
// combined with AllowCredentials: true. A pattern like
// "https://*.example.com" expands to a concrete origin at
// request time, so browsers permit credentials for it; the
// guard is intentionally narrow.
var CorsOriginPattern = regexp.MustCompile(`^(?:\*|https?://(?:\*\.[a-zA-Z0-9.\-]+|localhost)(?::\*|\:[0-9]+)?|https?://[a-zA-Z0-9.\-]+(?::\*|\:[0-9]+)?)$`)

func (a *EdgeRuleCORSAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("cors action is required")
	}
	// ADR-129 D2 mutual exclusivity. When CorsPresetID is set,
	// the inline fields MUST be empty/zero — the preset is the
	// entire policy. Allowing inline fields alongside a preset
	// would let an inline override silently shadow the preset
	// (the merge helper's "rule-wins-zero-overrides-preset"
	// convention would apply per-field, which is the surprising
	// behaviour ADR-129 D2 rejects). The customer either uses
	// the preset OR the inline fields, not both.
	if a.CorsPresetID != nil {
		if len(a.AllowOrigins) > 0 {
			return ErrValidation("cors action cannot combine cors_preset_id with inline allow_origins — pick one (ADR-129 D2 mutual exclusivity)")
		}
		if len(a.AllowMethods) > 0 {
			return ErrValidation("cors action cannot combine cors_preset_id with inline allow_methods — pick one (ADR-129 D2 mutual exclusivity)")
		}
		if len(a.AllowHeaders) > 0 {
			return ErrValidation("cors action cannot combine cors_preset_id with inline allow_headers — pick one (ADR-129 D2 mutual exclusivity)")
		}
		if len(a.ExposeHeaders) > 0 {
			return ErrValidation("cors action cannot combine cors_preset_id with inline expose_headers — pick one (ADR-129 D2 mutual exclusivity)")
		}
		if a.AllowCredentials {
			return ErrValidation("cors action cannot combine cors_preset_id with inline allow_credentials — pick one (ADR-129 D2 mutual exclusivity)")
		}
		if a.MaxAgeSeconds != 0 {
			return ErrValidation("cors action cannot combine cors_preset_id with inline max_age_seconds — pick one (ADR-129 D2 mutual exclusivity)")
		}
		return nil
	}
	if len(a.AllowOrigins) == 0 {
		return ErrValidation("cors action requires at least one allow_origin")
	}
	if len(a.AllowMethods) == 0 {
		return ErrValidation("cors action requires at least one allow_method")
	}
	// CORS improvements D6: cap MaxAgeSeconds at 24h. Browsers
	// ignore larger values; the gateway was happily stamping
	// "Access-Control-Max-Age: 2147483647" before the cap.
	// The lower bound is unchanged (>= 0).
	if a.MaxAgeSeconds < 0 {
		return ErrValidation("cors action max_age_seconds must be >= 0")
	}
	if a.MaxAgeSeconds > 86400 {
		return ErrValidation("cors action max_age_seconds must be <= 86400 (24h; browsers ignore larger values)")
	}
	// CORS improvements D2: validate every allow_origins entry
	// against the CorsOriginPattern grammar. The gateway's
	// matchOrigin (handler.go) applies the same predicates, so
	// a rule that bypasses the apid validator still matches
	// what the customer expects.
	for _, origin := range a.AllowOrigins {
		if !CorsOriginPattern.MatchString(origin) {
			return ErrValidation(
				"cors action allow_origin " + strconv.Quote(origin) +
					" does not match the supported grammar: bare \"*\", literal \"https://host[:port]\"," +
					" subdomain wildcard \"https://*.host\", or port wildcard \"https://host:*\"")
		}
	}
	// CORS *+credentials footgun: browsers reject the combination
	// Access-Control-Allow-Origin: * together with Access-Control-
	// Allow-Credentials: true (RFC 6454 §7). Reject at create-time
	// rather than shipping a rule that silently fails in production.
	// (ADR-091 D12.) Only the bare "*" entry trips the guard — a
	// subdomain/port wildcard expands to a concrete origin at
	// request time and is credentials-safe.
	if a.AllowCredentials {
		for _, origin := range a.AllowOrigins {
			if origin == "*" {
				return ErrValidation("cors action cannot combine AllowCredentials: true with AllowOrigins: [\"*\"] (browsers reject this combination)")
			}
		}
	}
	return nil
}

// edgeRuleJWTAllowedAlgs is the closed asymmetric-algorithm
// vocabulary the gateway's verifier accepts. HS* is intentionally
// excluded: HS* over JWKS would mean a symmetric key served from a
// public endpoint, where anyone with the URL can forge tokens.
// Customers needing HMAC-signed JWTs should use a separate secret
// reference action shape (deferred to a future ADR — ADR-091 D11).
var edgeRuleJWTAllowedAlgs = map[string]struct{}{
	"RS256": {}, "RS384": {}, "RS512": {},
	"ES256": {}, "ES384": {}, "ES512": {},
}

// EdgeRuleJWTAction validates an inbound Bearer JWT.
type EdgeRuleJWTAction struct {
	Issuer         string            `json:"issuer"`
	Audience       []string          `json:"audience,omitempty"`
	JWKSURL        string            `json:"jwks_url"`
	Algorithms     []string          `json:"algorithms"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
}

// edgeRuleJWTAllowedJWKSURLPrefixes is the closed list of prefixes
// the validator rejects to prevent the gateway from being tricked
// into fetching JWKS over a private/loopback/link-local address.
// The firewall already denies egress to these ranges (CLAUDE.md
// §11); this is the application-layer equivalent (ADR-091 D10).
// Future enhancement: promote to net.ParseIP + IsPrivate/IsLoopback
// /IsLinkLocalUnicast for IPv6-multicast edges.
var edgeRuleJWTAllowedJWKSURLPrefixes = []string{
	"https://localhost",
	"https://localhost.",
	"https://127.",
	"https://10.",
	"https://192.168.",
	"https://169.254.",
	"https://[::",
	"https://[fc",
	"https://[fd",
	// IPv4 link-local: 169.254.0.0/16 (covered above).
}

func (a *EdgeRuleJWTAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("jwt action is required")
	}
	if a.Issuer == "" {
		return ErrValidation("jwt action requires issuer")
	}
	if !strings.HasPrefix(a.JWKSURL, "https://") {
		return ErrValidation("jwt action jwks_url must start with https://")
	}
	// Defense-in-depth: reject JWKS URLs that resolve to
	// private/loopback/link-local addresses (ADR-091 D10). The
	// string-prefix check is cheap; a future enhancement can upgrade
	// to net.ParseIP + IsPrivate/IsLoopback/IsLinkLocalUnicast.
	lower := strings.ToLower(a.JWKSURL)
	for _, badPrefix := range edgeRuleJWTAllowedJWKSURLPrefixes {
		if strings.HasPrefix(lower, badPrefix) {
			return ErrValidation("jwt action jwks_url must not point to a private/loopback/link-local address (§11 egress posture)")
		}
	}
	if len(a.Algorithms) == 0 {
		return ErrValidation("jwt action requires at least one algorithm")
	}
	for _, alg := range a.Algorithms {
		if _, ok := edgeRuleJWTAllowedAlgs[alg]; !ok {
			return ErrValidation(fmt.Sprintf("jwt action algorithm %q is not in the closed vocabulary (RS256/RS384/RS512/ES256/ES384/ES512)", alg))
		}
	}
	return nil
}

// EdgeRuleIPAction is a CIDR allow/deny evaluator.
type EdgeRuleIPAction struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func (a *EdgeRuleIPAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("ip action is required")
	}
	if len(a.Allow) == 0 && len(a.Deny) == 0 {
		return ErrValidation("ip action requires at least one allow or deny entry")
	}
	for _, cidr := range a.Allow {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return ErrValidation(fmt.Sprintf("ip action allow entry %q is not a valid CIDR: %v", cidr, err))
		}
	}
	for _, cidr := range a.Deny {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return ErrValidation(fmt.Sprintf("ip action deny entry %q is not a valid CIDR: %v", cidr, err))
		}
	}
	return nil
}

// edgeRuleValidateRefURLPattern matches external `$ref` / `$id` values
// that we refuse to compile against. The capture group is the URL
// substring (so the 422 detail can name it); an external reference is
// any non-empty URL that does NOT resolve to a JSON Pointer (the
// `#/foo/bar` form). The JWKS-URL defence-in-depth at ADR-091 D10
// uses the same posture but a different regex shape; this is the
// JSON Schema analogue.
//
// Conservative on purpose: we strip / refuse anything that looks
// URL-shaped on the right-hand side of `$ref` / `$id`. Internal
// pointers (`#/definitions/Foo`) pass through. Same posture as the
// §11 egress firewall — a customer cannot smuggle a request to
// RFC1918 / metadata ranges through `$ref` resolution at hot-path
// compile time. pkg/edgevalidate re-strips at compile time as
// defence-in-depth.
//
// PR-C: anchored the key. The previous `\$ref|id` alternation
// matched the substring `id` anywhere in the schema — including
// inside `definitions` (so `{"$ref": "#/definitions/Foo"}` was
// wrongly rejected as if it were an external URL). The new regex
// requires the key to be a top-level JSON property name ("$ref"
// or "$id"), followed by an optional-whitespace colon, followed by
// a URL-shaped value. Verified against 10 cases in this PR-C
// harness (see plan-file regression-proof walkthrough).
var edgeRuleValidateRefURLPattern = regexp.MustCompile(`"\s*(\$ref|\$id)\s*"\s*:\s*"(https?://|//)[^"]+"`)

// EdgeRuleValidateAction is the wire shape for a kind=validate edge
// rule. Schema is a JSON Schema 2020-12 document (Draft 2020-12;
// sanity-bounded to a closed keyword set so a customer cannot ship
// a vocabulary we don't compile). The apid-side Validate() runs
// first; pkg/edgevalidate.Compile re-validates at compile time on
// the gateway hot path as defence-in-depth.
//
// Field-by-field:
//
//   - Schema: required JSON Schema document. Capped at
//     MaxEdgeRuleValidateSchemaBytes (64 KiB). External `$ref` /
//     `$id` URLs are rejected — see edgeRuleValidateRefURLPattern.
//   - ContentTypes: optional media-type allowlist (e.g.
//     ["application/json"]). Closed set application/* (the
//     spec runtime is JSON; non-JSON schemas are out of scope
//     for v1). Empty = match any Content-Type.
//   - ApplyWhileStreaming: per-rule opt-in for the streaming
//     response path (ADR-047). Default false mirrors the §4.1
//     Accept: application/json opt-out (an SSE-enabled app keeps
//     validation off until the customer opts the rule in).
//   - RejectOnUnknownFields: toggles additionalProperties=false
//     on the compiled schema so a body with stray fields fails.
//     Default false preserves byte-stable schemas.
//   - MaxBodyBytes: per-rule inbound body cap. 0 = inherit
//     api.MaxRequestBodyBytes (per-plan 25 MB buffered / 100 MB
//     streaming). Must be > 0 and <= MaxRequestBodyBytes at
//     create-time.
//   - ValidateMode: DEPRECATED (ADR-128 D2). Moved to top-level
//     `EdgeRuleResponse.validate_mode` / `CreateEdgeRuleRequest.
//     validate_mode` / `UpdateEdgeRuleRequest.validate_mode`. This
//     action-level field is retained for the back-compat read
//     window — older clients may still send it here and older
//     decoders may still read it from a response. The field will
//     be removed in the release after the deprecation notice.
type EdgeRuleValidateAction struct {
	Schema                json.RawMessage `json:"schema"`
	ContentTypes          []string        `json:"content_types,omitempty"`
	ApplyWhileStreaming   bool            `json:"apply_while_streaming,omitempty"`
	RejectOnUnknownFields bool            `json:"reject_on_unknown_fields,omitempty"`
	MaxBodyBytes          int             `json:"max_body_bytes,omitempty"`
	ValidateMode          string          `json:"validate_mode,omitempty"`
}

// ValidateMode values (issue #975 #3 / Mega-Foundation #979-a).
// The set is small and closed; the gateway defaults to 'block'
// when the rule row predates the column migration.
const (
	ValidateModeBlock   = "block"
	ValidateModeObserve = "observe"
	ValidateModeWarn    = "warn"
)

func (a *EdgeRuleValidateAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("validate action is required")
	}
	if len(a.Schema) == 0 {
		return ErrValidation("validate action: schema is required")
	}
	if len(a.Schema) > MaxEdgeRuleValidateSchemaBytes {
		return ErrValidation(fmt.Sprintf(
			"validate action: schema exceeds %d bytes (got %d)",
			MaxEdgeRuleValidateSchemaBytes, len(a.Schema)))
	}
	// JSON well-formedness check. The json/v6 compile path runs
	// this again at gateway compile time, but a fast-fail here keeps
	// a malformed schema out of the apid create path and out of the
	// 64 KiB-ish jsonb blob (Postgres jsonb rejects malformed JSON
	// at insert with a 22P02 — same shape, but earlier).
	var probe any
	if err := json.Unmarshal(a.Schema, &probe); err != nil {
		return ErrValidation(fmt.Sprintf("validate action: schema is not valid JSON: %v", err))
	}
	// External-$ref/$id strip. The JWKS-URL guard at ADR-091 D10
	// uses the same posture: refuse any URL-shaped value rather
	// than try to enumerate safe hosts (a regex strip is cheaper
	// to audit). The gateway side re-strips at compile time as
	// defence-in-depth.
	if match := edgeRuleValidateRefURLPattern.FindStringIndex(string(a.Schema)); match != nil {
		return ErrValidation(fmt.Sprintf(
			"validate action: schema contains an external $ref or $id URL (around byte %d); inline schemas only",
			match[0]))
	}
	// ContentTypes: optional; closed set application/* (the spec
	// runtime is JSON for v1). Empty == match any; non-empty must
	// every entry start with the `application/` prefix and not be
	// `application/*` (which is what json/* would mean in a future
	// release; deferred to a new ADR).
	for _, ct := range a.ContentTypes {
		if !strings.HasPrefix(ct, "application/") {
			return ErrValidation(fmt.Sprintf(
				"validate action: content_types entries must start with 'application/' (got %q)",
				ct))
		}
	}
	// MaxBodyBytes: optional; clamped at create-time to the plan's
	// MaxRequestBodyBytes. We don't read plan limits here (this
	// runs in dto.go, plan-agnostic); the apid handler clamps after
	// Validate returns so a customer can ship MaxBodyBytes=0 without
	// the dto validator complaining. The hard upper bound is the
	// platform cap.
	if a.MaxBodyBytes < 0 {
		return ErrValidation(fmt.Sprintf(
			"validate action: max_body_bytes must be >= 0 (got %d)", a.MaxBodyBytes))
	}
	if a.MaxBodyBytes > MaxRequestBodyBytes {
		return ErrValidation(fmt.Sprintf(
			"validate action: max_body_bytes exceeds the platform cap (%d > %d)",
			a.MaxBodyBytes, MaxRequestBodyBytes))
	}
	// ValidateMode: optional; empty == 'block' (the strictest mode,
	// matches the NOT NULL DEFAULT 'block' the migration adds at
	// 00293). Any non-empty value must be one of the three closed
	// strings; an unknown value gets a 422 with the allowed list,
	// not a 500.
	if a.ValidateMode != "" &&
		a.ValidateMode != ValidateModeBlock &&
		a.ValidateMode != ValidateModeObserve &&
		a.ValidateMode != ValidateModeWarn {
		return ErrValidation(fmt.Sprintf(
			"validate action: validate_mode must be one of %q, %q, %q (got %q)",
			ValidateModeBlock, ValidateModeObserve, ValidateModeWarn, a.ValidateMode))
	}
	return nil
}

// EdgeRuleLimitAction is the wire shape for a kind=limit edge rule
// (ADR-091 D24). The standalone body-size primitive: a customer
// who only wants per-route body-size protection ("POST /upload
// ≤ 5 MB, POST /users ≤ 1 MB, POST /webhooks ≤ 2 MB") declares
// this kind without shipping a JSON Schema. The hot-path applier
// (pkg/gateway.(*Handler).applyEdgeRuleLimit, §4.1.2.8c) installs
// http.MaxBytesReader on r.Body at the per-rule cap and short-
// circuits oversize requests with 413 request_too_large — and,
// more importantly, performs a Content-Length fast-path deny so
// a 30 MB body on a 5 MB cap costs zero bytes of buffering.
//
// Field-by-field:
//
//   - MaxBodyBytes: required buffered-path cap. Must be > 0 and
//     ≤ MaxRequestBodyBytes (25 MiB). 0 is rejected with 422 —
//     a standalone limit rule with no cap is a silent no-op,
//     worst shape for a security feature. The hard upper bound
//     matches MaxRequestBodyBytes so a kind=limit rule can never
//     widen past the global cap; if the customer wants to relax
//     the cap on a specific path they're using the wrong
//     primitive (this kind is strictly a tightening primitive).
//   - MaxBodyBytesStreaming: optional streaming opt-in cap (≤
//     MaxEdgeRuleLimitBodyBytesStreaming = 100 MiB, ADR-080
//     raw-bridge parity). 0 = no streaming carve-out, the
//     buffered MaxBodyBytes is the cap on both paths. Must be
//     ≥ MaxBodyBytes when set — a streaming cap that is
//     TIGHTER than the buffered cap would 413 every streaming
//     request for a body that was already accepted as buffered,
//     which is a wire-shape footgun. Runtime enforcement of
//     this field is deferred to a follow-up PR (stated in
//     ADR-091 D24 §6); the field is declared, clamped here, and
//     clamped again at cmd-side compileLimitRules so a future
//     runbook can wire enforcement without schema churn.
type EdgeRuleLimitAction struct {
	MaxBodyBytes          int `json:"max_body_bytes"`
	MaxBodyBytesStreaming int `json:"max_body_bytes_streaming,omitempty"`
}

func (a *EdgeRuleLimitAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("limit action is required")
	}
	if a.MaxBodyBytes <= 0 {
		return ErrValidation(fmt.Sprintf(
			"limit action: max_body_bytes must be > 0 (got %d) — a standalone limit rule with no cap is a silent no-op; use kind=validate if you need a body cap alongside a JSON Schema",
			a.MaxBodyBytes))
	}
	if a.MaxBodyBytes > MaxRequestBodyBytes {
		return ErrValidation(fmt.Sprintf(
			"limit action: max_body_bytes exceeds the platform cap (%d > %d)",
			a.MaxBodyBytes, MaxRequestBodyBytes))
	}
	if a.MaxBodyBytesStreaming < 0 {
		return ErrValidation(fmt.Sprintf(
			"limit action: max_body_bytes_streaming must be >= 0 (got %d)",
			a.MaxBodyBytesStreaming))
	}
	if int64(a.MaxBodyBytesStreaming) > MaxEdgeRuleLimitBodyBytesStreaming {
		return ErrValidation(fmt.Sprintf(
			"limit action: max_body_bytes_streaming exceeds the streaming platform cap (%d > %d)",
			a.MaxBodyBytesStreaming, MaxEdgeRuleLimitBodyBytesStreaming))
	}
	if a.MaxBodyBytesStreaming > 0 && a.MaxBodyBytesStreaming < a.MaxBodyBytes {
		return ErrValidation(fmt.Sprintf(
			"limit action: max_body_bytes_streaming (%d) must be >= max_body_bytes (%d) when set — a streaming cap tighter than the buffered cap would 413 every streaming request for a body already accepted as buffered",
			a.MaxBodyBytesStreaming, a.MaxBodyBytes))
	}
	return nil
}

// EdgeRuleGeoAction is an ISO 3166-1 alpha-2 country allow/deny
// evaluator. Allow entries are ISO 3166-1 alpha-2 codes ("DE", "FR",
// "US"). Deny is evaluated AFTER allow so a single-country deny
// sticks even when the allow list is broad — mirrors the EdgeRuleIPAction
// evaluate order so the §4.1.2 matcher has a single consistent
// "deny walks last" rule across both primitives.
//
// The gateway is wired to a DB-IP Lite CC-BY-4.0 country database
// (see pkg/geoip). The DB-IP dataset covers the ~249 ISO 3166-1
// alpha-2 sovereign-state codes. The user-assigned reserved codes
// (AA, ZZ, etc.) are NOT in the public DB set — accepting them in
// Validate would silently fail-open at request time (the DB-IP
// DB returns no country for an unknown code, the matcher
// fail-opens, and the customer's "block ZZ" rule never fires).
//
// Validate uses a 2-tier shape check: (1) 2-letter ASCII alpha
// (case-insensitive); (2) explicit rejection of the 39
// user-assigned / reserved codes (AA, ZZ, etc.); (3) explicit
// rejection of the 5 "exceptionally reserved" codes (AC, EU, UN,
// etc. — these are valid in ISO 3166-1 but not country codes per
// se, and the DB-IP DB does not return them). Codes that pass
// (1)+(2)+(3) are forwarded to the gateway for the DB-IP lookup;
// the lookup itself is fail-open.
//
// Plan-tier quota is enforced separately in the apid handler via
// Limits.EdgeRulesGeoPerApp — this validator only checks shape.
type EdgeRuleGeoAction struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// edgeRuleGeoReservedCodes is the set of ISO 3166-1 alpha-2 codes
// that are NOT country codes: user-assigned codes (AA, ZZ, etc.),
// exceptionally reserved codes (AC, EU, UN, etc.), and the
// transitional reservations (AN was deleted 2010 but remains
// reserved for backward compatibility, and similar). The DB-IP
// Lite database does not map any of these to a country, so
// accepting them in Validate would silently fail-open at request
// time.
//
// The reserved set is built once at package load. The full set of
// ISO 3166-1 alpha-2 reserved/transitional codes is documented at
// https://en.wikipedia.org/wiki/ISO_3166-1_alpha-2 (Reserved_code
// + Exceptionally_reserved sections).
var edgeRuleGeoReservedCodes = func() map[string]struct{} {
	m := make(map[string]struct{}, 50)
	for _, c := range []string{
		// User-assigned (29 codes, AA–ZZ excluding the 26
		// real letters that ARE assigned). AA, ZZ, plus the
		// 27 ranges like OY–ZZ we list explicitly so the
		// table is greppable.
		"AA", "ZZ",
		// Exceptionally reserved (5 codes — multi-national
		// organizations and ISO 3166 maintenance).
		"AC", "EU", "FX", "SU", "UK", "UN",
		// Transitional reservations (8 codes — formerly
		// assigned, deleted but still reserved so historical
		// data does not alias).
		"AN", "BU", "CS", "DD", "NT", "SC", "TP", "YU", "ZR",
		// ISO 3166-1 numeric-only code "XX" — sometimes
		// emitted as "XX" in some databases.
		"XX",
		// Misc.
		"CP", "DG", "EA", "IC", "TA", "WG",
	} {
		m[c] = struct{}{}
	}
	return m
}()

func (a *EdgeRuleGeoAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("geo action is required")
	}
	if len(a.Allow) == 0 && len(a.Deny) == 0 {
		return ErrValidation("geo action requires at least one allow or deny entry")
	}
	seen := make(map[string]struct{}, len(a.Allow)+len(a.Deny))
	for _, code := range a.Allow {
		if p := validateGeoCountryCode(code); p != nil {
			return ErrValidation("geo action allow entry " + p.Error())
		}
		upper := strings.ToUpper(code)
		if _, dup := seen[upper]; dup {
			return ErrValidation(fmt.Sprintf("geo action allow entry %q is duplicated; each country code may appear at most once across allow+deny", code))
		}
		seen[upper] = struct{}{}
	}
	for _, code := range a.Deny {
		if p := validateGeoCountryCode(code); p != nil {
			return ErrValidation("geo action deny entry " + p.Error())
		}
		upper := strings.ToUpper(code)
		if _, dup := seen[upper]; dup {
			return ErrValidation(fmt.Sprintf("geo action deny entry %q is duplicated; each country code may appear at most once across allow+deny", code))
		}
		seen[upper] = struct{}{}
	}
	// Per-rule cardinality cap. A single geo rule with 200+ entries
	// looks like a customer's mistake (the DB-IP dataset has ~249
	// codes). Cap at 50 — well above the realistic "block
	// everywhere except these 5 EU countries" use case, well below
	// the 249-entry cap that would make the rule functionally a
	// "deny everything" sentinel.
	if len(seen) > 50 {
		return ErrValidation(fmt.Sprintf("geo action allow+deny entry count = %d, want ≤ 50 (a single rule with 50+ country codes looks like a configuration mistake; split into multiple rules or use a allowlist of jurisdictions, not the full closed vocab)", len(seen)))
	}
	return nil
}

// validateGeoCountryCode returns nil if code is a well-formed
// ISO 3166-1 alpha-2 country code that pkg/geoip.Reader can
// resolve at request time, otherwise an error message suitable
// for surfacing in an RFC 7807 detail block.
//
// The two-tier check is:
//
//  1. Shape: exactly 2 ASCII letters, uppercased.
//  2. Reserved/non-country: rejects the user-assigned,
//     exceptionally-reserved, and transitional-reserved codes
//     listed in edgeRuleGeoReservedCodes.
//
// Codes that pass both checks are forwarded to the gateway
// for the DB-IP lookup; the lookup itself is fail-open.
func validateGeoCountryCode(code string) error {
	if len(code) != 2 {
		return fmt.Errorf("%q is not a 2-letter ISO 3166-1 alpha-2 country code (got length %d)", code, len(code))
	}
	upper := strings.ToUpper(code)
	c0, c1 := upper[0], upper[1]
	if c0 < 'A' || c0 > 'Z' || c1 < 'A' || c1 > 'Z' {
		return fmt.Errorf("%q is not a 2-letter ISO 3166-1 alpha-2 country code (got non-letter bytes %q / %q)", code, string(c0), string(c1))
	}
	if _, reserved := edgeRuleGeoReservedCodes[upper]; reserved {
		return fmt.Errorf("%q is a reserved/user-assigned ISO 3166-1 alpha-2 code (the DB-IP Lite database does not map reserved codes to a country; accepting it would silently fail-open at request time)", code)
	}
	return nil
}

// EdgeRuleMaintenanceAction is the wire shape for a kind=maintenance
// edge rule (ADR-091 amendment, PR-A #???). The customer-facing
// primitive for "this route is in maintenance mode" — the hot-path
// applier (pkg/gateway.(*Handler).applyEdgeRuleMaintenance,
// §4.1.2.13) short-circuits a matched (host, path, http_method)
// request with 503 + Retry-After before the wake gate. The coarse
// sibling (apps.maintenance_mode) is the per-app version and lives
// on the apps row directly (MaintenanceMode *bool on CreateAppRequest
// / UpdateAppRequest), so this DTO is only for the fine-grained
// per-route case.
//
// Field-by-field:
//
//   - RetryAfterSeconds: optional override for the per-rule
//     Retry-After header. 0 means "use the platform default
//     EdgeRuleMaintenanceRetryAfterSeconds (60 s)". Must be in
//     [0, MaxEdgeRuleMaintenanceRetryAfterSeconds] (24 h) — a
//     customer cannot ship a rule that asks a client to back off
//     for a week. Negative values rejected with 422; values above
//     the cap rejected with 422.
//   - Message: optional operator-friendly string that goes into
//     Problem.detail. ≤ 512 B; same payload-size budget as
//     EdgeRuleValidateAction.Schema. Newlines / control bytes are
//     not sanitised at this layer (slog JSON re-encodes the wire
//     Problem at log time) — the limit is bytes-on-the-wire, not
//     a sanitiser.
type EdgeRuleMaintenanceAction struct {
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	Message           string `json:"message,omitempty"`
}

func (a *EdgeRuleMaintenanceAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("maintenance action is required")
	}
	if a.RetryAfterSeconds < 0 {
		return ErrValidation(fmt.Sprintf(
			"maintenance action: retry_after_seconds must be >= 0 (got %d)",
			a.RetryAfterSeconds))
	}
	if a.RetryAfterSeconds > MaxEdgeRuleMaintenanceRetryAfterSeconds {
		return ErrValidation(fmt.Sprintf(
			"maintenance action: retry_after_seconds exceeds the platform cap (%d > %d)",
			a.RetryAfterSeconds, MaxEdgeRuleMaintenanceRetryAfterSeconds))
	}
	if len(a.Message) > 512 {
		return ErrValidation(fmt.Sprintf(
			"maintenance action: message exceeds 512 bytes (got %d)",
			len(a.Message)))
	}
	return nil
}

// EdgeRuleThrottleAction is the wire shape for a kind=throttle edge
// rule (ADR-091 D20.5 amendment, issue #881). The per-route
// token-bucket primitive: customers tighten the per-route rps/burst
// below their plan's plan.RateLimitRPS — the apid validator enforces
// the sub-plan ceiling; the gateway compiler enforces it again at
// load time.
//
// Sub-plan ceiling — the load-bearing constraint. A throttle rule is
// STRICTLY a tightening primitive. The per-handler parameter passed
// by the apid handler is the customer's plan row; the ceiling is
// plan.RateLimitRPS / plan.RateLimitBurst. A rule that exceeds the
// ceiling is rejected with 422 BEFORE any DB write — a customer
// cannot raise their plan limit by registering a throttle rule.
//
// The wire shape uses float64 for RequestsPerSecond so the
// recommendation endpoint (which emits ceil(observed_rps * 2)) can
// hand over a non-integer without coercing the customer's intent.
// The bucket math spends the float directly in `tokens += dt *
// rps`, so fractional values are exact under the refill formula.
//
// Validate enforces:
//   - RequestsPerSecond > 0 (a 0-rps rule is a silent no-op AND
//     would create a bucket that never refills — that bucket is
//     OUT OF THE EVICTABLE SET per pkg/gateway/ratelimit.go's
//     full-bucket-only invariant, so it would be a permanent
//     memory leak).
//   - RequestsPerSecond ≤ plan.RateLimitRPS (sub-plan ceiling).
//   - Burst > 0 (same leak rationale as above).
//   - Burst ≤ plan.RateLimitBurst (sub-plan ceiling).
//
// Per-IP sub-keying is deliberately absent in v1 — see
// pkg/state/types.go::EdgeRuleThrottleAction for the design rationale.
//
// Phase 3 (ADR-091 D20.5 amendment 4, ADR-104, issue #881 Phase 3)
// extends the wire shape with optional per-consumer keying. The new
// fields default to zero-values that produce bit-identical behaviour
// to PR #887's bucket key (appID+"\x00"+ruleID):
//
//   - KeyBy ∈ {"", "none", "api_key", "jwt_subject", "jwt_claim"}.
//     Empty string and "none" are equivalent — the empty value is the
//     pre-Phase-3 shape; "none" is the explicit Phase-3 opt-out. Both
//     preserve back-compat (the bucket key is unchanged).
//   - JWTClaimName is REQUIRED iff KeyBy == "jwt_claim"; the value
//     names the JWT custom claim to extract (e.g., "tier", "org_id").
//     Format constraint mirrors CodeQL `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`.
//   - MaxKeysPerRule caps how many distinct consumers may own a
//     bucket against this rule. Distinct consumers above the cap
//     collapse into a single non-evicting "__other__" bucket that
//     STILL consumes tokens (ADR-104 §"Consequences" load-bearing
//     safety property). Defaults to 0 meaning "use plan default".
//
// The bounded design is enforced at the limiter layer
// (pkg/gateway/ratelimit.go::AllowWithConsumerKey, Phase 3). The
// apid validator only checks shape + plan ceiling; the limiter owns
// the actual collapse semantics.
type EdgeRuleThrottleAction struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
	KeyBy             string  `json:"key_by,omitempty"`
	JWTClaimName      string  `json:"jwt_claim_name,omitempty"`
	MaxKeysPerRule    int     `json:"max_keys_per_rule,omitempty"`
}

// ThrottleKeyByNone is the explicit Phase-3 opt-out value. The empty
// string "" and "none" both preserve PR #887's behaviour — the
// difference is that "none" makes the customer's intent visible in the
// wire payload (debugging + audit). The validator treats "" and "none"
// as equivalent; the limiter constructor never sees the empty value
// because the wire→state mapping normalises "" → "none".
const (
	ThrottleKeyByNone       = "none"
	ThrottleKeyByAPIKey     = "api_key"
	ThrottleKeyByJWTSubject = "jwt_subject"
	ThrottleKeyByJWTClaim   = "jwt_claim"
)

// ThrottleMaxKeysPerRuleDefault is the fallback MaxKeysPerRule the
// gateway uses when the resolved rule carries a zero — defensive
// against a direct-DB write that bypassed the apid-validator AND
// cmd-side compileThrottleRules. Matches the Hobby plan ceiling
// (pkg/api/limits.go::ThrottleMaxKeysPerRule) — middle of the
// ladder, neither Free-tight (100) nor Scale-loose (10_000). The
// cmd-side compile is the source of truth for the value the
// limiter actually uses at runtime; this constant exists so a
// misconfigured rule doesn't accidentally promote a per-consumer
// rule to unbounded cardinality (the worst-case attack surface).
const ThrottleMaxKeysPerRuleDefault = 1000

// ThrottleKeyByIsPerConsumer reports whether the supplied KeyBy
// value opts the rule into per-consumer bucket keying
// (ADR-104, issue #881 Phase 3). Empty string is treated as
// back-compat (PR #887's `appID+"\x00"+ruleID` shape) — only
// the explicit "none" and the four other close-vocab values
// trigger per-consumer routing. The single source of truth for
// "is this a per-consumer KeyBy?" — pkg/gateway/handler.go and
// cmd/gatewayd-internal/edge_rules.go both consult this rather
// than duplicating the membership test, so adding a future
// Phase 4 value (e.g. "ip") is a one-line constant + this helper
// update.
func ThrottleKeyByIsPerConsumer(keyBy string) bool {
	switch keyBy {
	case ThrottleKeyByAPIKey, ThrottleKeyByJWTSubject, ThrottleKeyByJWTClaim:
		return true
	default:
		return false
	}
}

// ThrottleValidationContext is the per-plan ceiling that
// validateEdgeRuleAction passes into EdgeRuleThrottleAction.Validate.
// Keeping the boundary explicit (rather than reading limits globally)
// makes the validator unit-testable without spinning up a plan row —
// see pkg/api/dto_edge_rules_test.go for the test pattern.
//
// PlanMaxKeysPerRule is the Phase 3 ceiling on per-rule consumer
// cardinality. 0 means the plan doesn't expose per-consumer throttling
// (the validator rejects any rule that opts into a non-"none" KeyBy).
type ThrottleValidationContext struct {
	PlanMaxRPS         float64
	PlanMaxBurst       int
	PlanMaxKeysPerRule int
}

// jwtClaimNameRegex pins the JWTClaimName format. Anchored, allows a
// leading letter or underscore, then [A-Za-z0-9_] up to 63 chars total.
// Mirrors the CodeQL go-clear-text-logging precedent for untrusted
// identifiers landing in metric labels and log fields — anything looser
// risks a label-cardinality explosion or a CodeQL finding on a future
// refactor.
var jwtClaimNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func (a *EdgeRuleThrottleAction) Validate(ctx ThrottleValidationContext) *Problem {
	if a == nil {
		return ErrValidation("throttle action is required")
	}
	if a.RequestsPerSecond <= 0 {
		// 0 / negative rps is rejected for two reasons: (1) it is a
		// silent no-op at request time — every request would be
		// dropped, indistinguishable from a misconfig; (2) the
		// bucket's refill formula uses rps as a multiplier, so a 0
		// rate means the bucket never refills — under
		// pkg/gateway/ratelimit.go's full-bucket-only invariant the
		// bucket is permanently unevictable, which is a memory leak.
		return ErrValidation(fmt.Sprintf(
			"throttle action: requests_per_second must be > 0 (got %g) — a 0-rps rule is a silent no-op AND would create a permanently unevictable bucket",
			a.RequestsPerSecond))
	}
	if a.Burst <= 0 {
		return ErrValidation(fmt.Sprintf(
			"throttle action: burst must be > 0 (got %d) — same rationale as a 0-rps rule",
			a.Burst))
	}
	if ctx.PlanMaxRPS > 0 && a.RequestsPerSecond > ctx.PlanMaxRPS {
		return ErrValidation(fmt.Sprintf(
			"throttle action: requests_per_second %g exceeds the plan ceiling %g — a throttle rule is strictly a tightening primitive; customers can only narrow, never widen",
			a.RequestsPerSecond, ctx.PlanMaxRPS))
	}
	if ctx.PlanMaxBurst > 0 && a.Burst > ctx.PlanMaxBurst {
		return ErrValidation(fmt.Sprintf(
			"throttle action: burst %d exceeds the plan ceiling %d — a throttle rule is strictly a tightening primitive",
			a.Burst, ctx.PlanMaxBurst))
	}
	// Phase 3 (ADR-104): per-consumer keying validation. KeyBy is
	// optional; the empty value preserves PR #887's behaviour and
	// needs no further checks. Non-empty values must be in the closed
	// vocab + require the right supporting field.
	switch a.KeyBy {
	case "", ThrottleKeyByNone:
		// Pre-Phase-3 shape: bucket key is appID+"\x00"+ruleID.
		// JWTClaimName and MaxKeysPerRule MUST be unset — a customer
		// mixing "key_by=none" with non-zero MaxKeysPerRule would
		// silently never trip the cap, which is a footgun.
		if a.JWTClaimName != "" {
			return ErrValidation("throttle action: jwt_claim_name requires key_by=\"jwt_claim\" (got key_by=\"\")")
		}
		if a.MaxKeysPerRule != 0 {
			return ErrValidation("throttle action: max_keys_per_rule requires key_by != \"none\" (got key_by=\"\")")
		}
	case ThrottleKeyByAPIKey, ThrottleKeyByJWTSubject:
		if a.JWTClaimName != "" {
			return ErrValidation(fmt.Sprintf(
				"throttle action: jwt_claim_name is only valid with key_by=\"jwt_claim\" (got key_by=%q)",
				a.KeyBy))
		}
		if err := validateThrottleMaxKeys(a.MaxKeysPerRule, ctx.PlanMaxKeysPerRule); err != nil {
			return err
		}
	case ThrottleKeyByJWTClaim:
		if a.JWTClaimName == "" {
			return ErrValidation("throttle action: jwt_claim_name is required when key_by=\"jwt_claim\"")
		}
		if !jwtClaimNameRegex.MatchString(a.JWTClaimName) {
			return ErrValidation(fmt.Sprintf(
				"throttle action: jwt_claim_name %q must match ^[a-zA-Z_][a-zA-Z0-9_]{0,63}$ (CodeQL safe-identifier contract)",
				a.JWTClaimName))
		}
		if err := validateThrottleMaxKeys(a.MaxKeysPerRule, ctx.PlanMaxKeysPerRule); err != nil {
			return err
		}
	default:
		return ErrValidation(fmt.Sprintf(
			"throttle action: key_by %q is not in the closed vocab (allowed: \"\", \"none\", \"api_key\", \"jwt_subject\", \"jwt_claim\")",
			a.KeyBy))
	}
	return nil
}

// validateThrottleMaxKeys enforces the per-rule MaxKeysPerRule ceiling.
//
//   - MaxKeysPerRule < 0 is a shape error.
//   - MaxKeysPerRule == 0 means "use plan default" — the limiter layer
//     resolves the default at run time; the validator only rejects
//     values above the plan ceiling when the plan has one.
//   - planMax == 0 means the plan doesn't expose per-consumer throttling
//     AND the customer is opting into per-consumer keying — that
//     combination is rejected up front. Without this guard a customer
//     could set key_by="api_key" with max_keys_per_rule=0 and silently
//     get the plan-default behaviour, which on a plan with
//     PlanMaxKeysPerRule=0 would mean "no consumers tracked" — a
//     useless rule that wastes the throttle-quota slot. (See
//     ADR-104 §"Rejected alternatives — Server-side consumer allowlist
//     is deferred"; the closest analogous case is "rule whose runtime
//     effect is zero".)
func validateThrottleMaxKeys(maxKeys, planMax int) *Problem {
	if maxKeys < 0 {
		return ErrValidation(fmt.Sprintf(
			"throttle action: max_keys_per_rule must be >= 0 (got %d)",
			maxKeys))
	}
	if planMax == 0 {
		// Plan doesn't expose per-consumer throttling. The caller in
		// Validate() has already gated on key_by != "none", so reaching
		// here means the customer is opting into a feature their plan
		// doesn't support. Reject.
		return ErrValidation(
			"throttle action: this plan does not support per-consumer throttling — upgrade or set key_by=\"none\"")
	}
	if maxKeys > planMax {
		return ErrValidation(fmt.Sprintf(
			"throttle action: max_keys_per_rule %d exceeds the plan ceiling %d",
			maxKeys, planMax))
	}
	return nil
}

// EdgeRuleBudgetAction is the wire shape for a kind=budget edge rule
// (ADR-093 §Decision). The per-request wall-clock budget primitive:
// a customer pins a hard wall-clock deadline on `POST /payment` →
// 3 s the same way they already pin JWT / IP / geo edge rules. The
// platform then propagates the remaining time to every downstream
// hop (DB, gRPC, outbound HTTP) and surfaces deadline fire as 504 +
// RFC 7807 `code: request_budget_exceeded`.
//
// Field-by-field:
//
//   - BudgetMs: required per-request budget in milliseconds. Must
//     be > 0 and ≤ api.RequestBudgetMaxMs (30 s). 0 is rejected
//     with 422 — a kind=budget rule with no budget is a silent
//     no-op, worst shape for a safety feature. The hard upper bound
//     matches api.RequestBudgetMaxMs so a kind=budget rule can
//     never widen past the platform ceiling; if a customer wants a
//     longer per-route budget they're mis-using this primitive
//     (api.RequestBudgetMax is the absolute ceiling).
//   - AllowOverrideHeader: optional HTTP header name (default
//     `x-faas-budget-ms`) whose numeric value, if present on the
//     inbound request, overrides BudgetMs for that single request.
//     A runtime per-customer-tunable knob layered on top of the
//     static rule. Header name must be 1..128 chars matching
//     `^[A-Za-z][A-Za-z0-9-]*$` (RFC 7230 token shape) — empty
//     is allowed (the runtime then uses the default
//     `x-faas-budget-ms`). An absent or unparseable header falls
//     through to BudgetMs unchanged.
//
// The runtime is `pkg/reqbudget`. The matched rule's BudgetMs
// stamps the inbound ctx via `reqbudget.WithRemaining`; every
// downstream hop wraps via `reqbudget.WithOverhead` / `WithCeiling`.
// The hot-path applier is
// pkg/gateway.(*Handler).applyEdgeRuleBudget (§4.1.2.8d).
type EdgeRuleBudgetAction struct {
	BudgetMs            int    `json:"budget_ms"`
	AllowOverrideHeader string `json:"allow_override_header,omitempty"`
}

func (a *EdgeRuleBudgetAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("budget action is required")
	}
	if a.BudgetMs <= 0 {
		return ErrValidation(fmt.Sprintf(
			"budget action: budget_ms must be > 0 (got %d) — a kind=budget rule with no budget is a silent no-op; drop the rule if you want the platform default to apply",
			a.BudgetMs))
	}
	if a.BudgetMs > int(RequestBudgetMax.Milliseconds()) {
		return ErrValidation(fmt.Sprintf(
			"budget action: budget_ms (%d) exceeds the platform ceiling (%d ms = %s)",
			a.BudgetMs, int(RequestBudgetMax.Milliseconds()), RequestBudgetMax))
	}
	if a.AllowOverrideHeader != "" {
		if len(a.AllowOverrideHeader) > 128 {
			return ErrValidation(fmt.Sprintf(
				"budget action: allow_override_header must be 1..128 chars (got %d)",
				len(a.AllowOverrideHeader)))
		}
		// RFC 7230 token shape — first char letter, rest
		// letters/digits/hyphens. Reject whitespace, commas, colons,
		// and other separator chars that would break header parsing.
		if !isHeaderToken(a.AllowOverrideHeader) {
			return ErrValidation(fmt.Sprintf(
				"budget action: allow_override_header %q must match RFC 7230 token shape (^[A-Za-z][A-Za-z0-9-]*$)",
				a.AllowOverrideHeader))
		}
	}
	return nil
}

// EdgeRuleCacheAction is the wire shape for a kind=cache edge rule
// (ADR-122 §Decision). Per-route TTL knobs for safe response caching
// on selected GET/HEAD paths. The state mirror is
// pkg/state/types.go::EdgeRuleCacheAction; the runtime is
// pkg/gateway/response_cache.go; the applier is
// pkg/gateway/handler_apply_edge_rule_cache.go.
//
// MaxAgeSeconds is the fresh window in seconds (default 60,
// allowed range [0, ResponseCacheMaxAgeMaxSeconds]). A zero value
// disables fresh hits but stale-on-error still applies within
// StaleIfErrorSeconds.
//
// StaleIfErrorSeconds is the post-fresh window during which a
// stored entry MAY be served ONLY on origin failure (wake gate
// failure or upstream 5xx/timeout). Hard cap
// ResponseCacheStaleIfErrorMaxSeconds (300 s). Exceeding the cap
// trips Validate.
//
// VaryOn is the closed set of non-credential header names whose
// values participate in the cache key. Closed vocabulary is
// edgeRuleCacheVaryOnVocab = {Accept-Language, Accept-Encoding};
// Authorization, Cookie, and any credential-bearing header are
// hard bypasses (never a key dimension). An empty slice means "no
// vary dimension beyond the URL" and is the default.
//
// Methods is the optional method allowlist (default {GET, HEAD}).
// Only idempotent methods are cacheable. Anything outside
// edgeRuleCacheMethodVocab trips Validate.
type EdgeRuleCacheAction struct {
	MaxAgeSeconds       int      `json:"max_age_seconds"`
	StaleIfErrorSeconds int      `json:"stale_if_error_seconds"`
	VaryOn              []string `json:"vary_on,omitempty"`
	Methods             []string `json:"methods,omitempty"`
}

// edgeRuleCacheVaryOnVocab is the closed vocabulary of headers that
// may participate in a kind=cache key. Closed by design: any
// credential-bearing header (Authorization, Cookie) would make a
// shared cache a cross-tenant leak. Per ADR-122 D3, authed requests
// are a hard bypass; vary_on is therefore restricted to
// non-credential discriminating dimensions.
var edgeRuleCacheVaryOnVocab = map[string]struct{}{
	"Accept-Language": {},
	"Accept-Encoding": {},
}

// edgeRuleCacheMethodVocab is the closed vocabulary of HTTP methods
// a kind=cache rule may target. Mirrors the cacheability predicate
// in pkg/gateway/response_cache.go. POST/PUT/PATCH/DELETE are
// deliberately absent — caching their responses is either
// incorrect (idempotency breaks under retry) or unsafe (cross-user
// state).
var edgeRuleCacheMethodVocab = map[string]struct{}{
	"GET":  {},
	"HEAD": {},
}

// ResponseCacheMaxAgeMaxSeconds is the absolute upper bound on a
// kind=cache rule's fresh window. 1 hour: long enough to amortise
// a wake across a real catalogue page-load session, short enough
// that a stale price/availability signal clears within a normal
// customer expectation window. Per-plan ceilings (which would be
// tighter) are deliberately NOT modelled in v1 — the absolute cap
// is sufficient to defend the in-process store.
const ResponseCacheMaxAgeMaxSeconds = 3600

// ResponseCacheStaleIfErrorMaxSeconds is the absolute upper bound
// on a kind=cache rule's stale-on-error window. 5 minutes matches
// the original ask; longer windows would let a stale body outlive
// a customer's reasonable expectation that an outage clears.
const ResponseCacheStaleIfErrorMaxSeconds = 300

// ResponseCacheDefaultMaxAgeSeconds is the apid-side default when
// a kind=cache rule omits max_age_seconds. 60 s matches the
// example in the ADR ask; a longer default would surprise users
// with stale prices; a shorter default would not pay back the
// wake-elision economics on a 5-min burst.
const ResponseCacheDefaultMaxAgeSeconds = 60

// ResponseCacheDefaultStaleIfErrorSeconds is the apid-side
// default when a kind=cache rule omits stale_if_error_seconds.
// 5 minutes matches the ADR ask's failure budget.
const ResponseCacheDefaultStaleIfErrorSeconds = 300

// Validate enforces the kind=cache invariants. The runtime
// cacheability predicate in pkg/gateway/response_cache.go is the
// second line of defence (e.g. dropping bodies larger than the
// per-entry cap); this method's job is to reject malformed
// configurations BEFORE the rule reaches the database.
func (a *EdgeRuleCacheAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("cache action is required")
	}
	// Apply apid-side defaults so the in-memory mirror in
	// pkg/state/types.go::EdgeRuleCacheAction carries explicit
	// values. The gateway compile step (commit 10) does NOT
	// re-default — a zero MaxAgeSeconds there is the
	// apid-submitted value, not "default". This makes "did the
	// customer ask for stale-on-error" auditable from the row.
	//
	// IMPORTANT: stale_if_error_seconds == 0 is the documented
	// "disable stale-on-error" semantic (per
	// docs/faas_implementation_spec.md §4.1.2.15); the apid
	// must NOT silently coerce it to 300. The
	// ResponseCacheDefaultStaleIfErrorSeconds constant stays
	// for callers that explicitly opt into the default (CLI
	// when the flag is absent); see CLI handling in
	// cmd/gregale/commands_edge_rules.go.
	if a.MaxAgeSeconds == 0 {
		a.MaxAgeSeconds = ResponseCacheDefaultMaxAgeSeconds
	}
	if a.MaxAgeSeconds < 0 {
		return ErrValidation(fmt.Sprintf(
			"cache action: max_age_seconds must be ≥ 0 (got %d) — a negative TTL would expire entries before they are stored",
			a.MaxAgeSeconds))
	}
	if a.MaxAgeSeconds > ResponseCacheMaxAgeMaxSeconds {
		return ErrValidation(fmt.Sprintf(
			"cache action: max_age_seconds (%d) exceeds the platform ceiling (%d s = 1 h); longer fresh windows amplify staleness risk and pin too much in-process memory",
			a.MaxAgeSeconds, ResponseCacheMaxAgeMaxSeconds))
	}
	if a.StaleIfErrorSeconds < 0 {
		return ErrValidation(fmt.Sprintf(
			"cache action: stale_if_error_seconds must be ≥ 0 (got %d)",
			a.StaleIfErrorSeconds))
	}
	if a.StaleIfErrorSeconds > ResponseCacheStaleIfErrorMaxSeconds {
		return ErrValidation(fmt.Sprintf(
			"cache action: stale_if_error_seconds (%d) exceeds the hard cap (%d s = 5 min); a longer stale window would outlive a customer's expectation that an outage clears",
			a.StaleIfErrorSeconds, ResponseCacheStaleIfErrorMaxSeconds))
	}
	// Methods defaults to {GET, HEAD} but may be empty (caller
	// chose not to enumerate). Reject anything outside the closed
	// vocab so a misconfigured rule cannot reach the gateway with
	// a method the cacheability predicate will silently drop.
	for _, m := range a.Methods {
		if _, ok := edgeRuleCacheMethodVocab[m]; !ok {
			return ErrValidation(fmt.Sprintf(
				"cache action: method %q is not in the closed cacheable-method vocabulary (GET, HEAD) — caching POST/PUT/PATCH/DELETE breaks idempotency or leaks state",
				m))
		}
	}
	// VaryOn must be a closed subset of the non-credential
	// vocabulary. Authorization / Cookie are deliberately NOT in
	// edgeRuleCacheVaryOnVocab so even a typo near
	// "Authorization" gives a clearer error than "credentialed
	// requests are a hard bypass" would on its own.
	for _, h := range a.VaryOn {
		if _, ok := edgeRuleCacheVaryOnVocab[h]; !ok {
			return ErrValidation(fmt.Sprintf(
				"cache action: vary_on header %q is not in the closed non-credential vocabulary (Accept-Language, Accept-Encoding) — credential-bearing headers are a hard bypass and cannot participate in a cache key",
				h))
		}
	}
	return nil
}

// isHeaderToken reports whether s matches the RFC 7230 token
// production used for HTTP header names: a letter followed by
// letters, digits, or hyphens. Used by EdgeRuleBudgetAction.Validate
// to reject malformed header-name overrides before the gateway
// sees them.
func isHeaderToken(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
		if i == 0 && (r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// EdgeRuleResponse is the wire shape for an edge rule. Action is
// kept as json.RawMessage so the generated Node/Python SDKs don't
// need seven per-kind models today; a typed SDK unmarshals into
// one of the seven structs based on Kind (deferred to a
// separate SDK ergonomics PR).
//
// ValidateMode is the top-level source of truth for kind=validate
// (ADR-128 D1). It is the resolved mode ('observe' | 'warn' |
// 'block'); the store's NOT NULL DEFAULT 'block' guarantees it
// is never empty. The action-level `validate_mode` field is
// retained for the back-compat read window (ADR-128 D2) but is
// deprecated and will be dropped in the release after the
// deprecation notice.
type EdgeRuleResponse struct {
	ID           string          `json:"id"`
	AccountID    string          `json:"account_id"`
	AppID        string          `json:"app_id"`
	MatchHost    string          `json:"match_host"`
	MatchPath    string          `json:"match_path"`
	MatchMethods []string        `json:"match_methods"`
	Priority     int             `json:"priority"`
	Enabled      bool            `json:"enabled"`
	Kind         string          `json:"kind"`
	ValidateMode string          `json:"validate_mode,omitempty"`
	Action       json.RawMessage `json:"action"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// CreateEdgeRuleRequest is the wire shape for POST /v1/apps/{slug}/edge-rules.
// Priority and Enabled are *int/*bool so the unset-vs-explicit
// distinction survives (the DTO is what reaches the store layer
// after pointer coercion).
//
// ValidateMode (ADR-128 D1) is the top-level source of truth for
// kind=validate. The handler prefers this field over the
// action-level `action.validate_mode` (deprecated). Empty == 'block'
// (the SQL-side default; the column is NOT NULL).
type CreateEdgeRuleRequest struct {
	MatchHost    string          `json:"match_host"`
	MatchPath    string          `json:"match_path"`
	MatchMethods []string        `json:"match_methods,omitempty"`
	Priority     *int            `json:"priority,omitempty"`
	Enabled      *bool           `json:"enabled,omitempty"`
	Kind         string          `json:"kind"`
	ValidateMode string          `json:"validate_mode,omitempty"`
	Action       json.RawMessage `json:"action"`
}

// UpdateEdgeRuleRequest is the wire shape for PATCH /v1/edge-rules/{id}.
// All fields are optional; nil = "leave alone".
//
// ValidateMode is *string so nil = "leave alone" (no row update).
// An explicit empty string is a no-op — the pgstore UPDATE runs
// coalesce(nullif($9, empty), validate_mode), which keeps the
// existing column value. Customers who want to reset to 'block'
// must send the explicit string "block".
type UpdateEdgeRuleRequest struct {
	MatchHost    *string          `json:"match_host,omitempty"`
	MatchPath    *string          `json:"match_path,omitempty"`
	MatchMethods *[]string        `json:"match_methods,omitempty"`
	Priority     *int             `json:"priority,omitempty"`
	Enabled      *bool            `json:"enabled,omitempty"`
	ValidateMode *string          `json:"validate_mode,omitempty"`
	Action       *json.RawMessage `json:"action,omitempty"`
}

// RekeyProgress is the response body of
// GET /v1/admin/secrets/rekey-progress (ADR-089 PR-C). It mirrors
// pkg/rekey.RekeyProgress exactly — same field names, same JSON tags,
// same int64 widths — so the on-disk FAAS_REKEY_PROGRESS_FILE and
// the admin response can share a decoder without a parallel type.
//
// The conversion (pkg/rekey.RekeyProgress → api.RekeyProgress) is a
// straight field-copy in cmd/apid/handlers_rekey.go::getRekeyProgress.
// Counters are cumulative across the walk and only grow; LastID is
// the resume cursor in (account_id|app_id|key) form.
type RekeyProgress struct {
	Total   int64  `json:"total"`
	Rekeyed int64  `json:"rekeyed"`
	Skipped int64  `json:"skipped"`
	Failed  int64  `json:"failed"`
	LastID  string `json:"last_id,omitempty"`
}

// OperatorIntentAcceptedResponse is the wire shape returned by
// POST /v1/admin/instances/{id}/force-park and
// POST /v1/admin/apps/{slug}/force-cold-boot (PR #1099 P2
// redesign). Both handlers now return 202 Accepted with an
// intent_id; the operator polls GET /v1/admin/operator-intents/{id}
// for terminal status. StatusURL is the relative path; clients
// prepend the apid base URL.
//
// InstanceID + PreviousState are populated for force_park;
// AppID + DeploymentID for force_cold_boot. Kind disambiguates
// which fields are meaningful. ExpiresAt is the recommended
// horizon for the operator to stop polling (5 minutes; matches
// the 30s safety tick + a comfortable buffer).
type OperatorIntentAcceptedResponse struct {
	OK            bool      `json:"ok"`
	IntentID      string    `json:"intent_id"`
	StatusURL     string    `json:"status_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	Kind          string    `json:"kind"`
	InstanceID    string    `json:"instance_id,omitempty"`
	AppID         string    `json:"app_id,omitempty"`
	DeploymentID  string    `json:"deployment_id,omitempty"`
	PreviousState string    `json:"previous_state,omitempty"`
	Reason        string    `json:"reason"`
}

// OperatorIntentResponse is the wire shape returned by
// GET /v1/admin/operator-intents/{id}. It mirrors the
// state.OperatorIntent struct directly. Status is one of
// "pending" | "running" | "succeeded" | "failed" | "cancelled".
// On success, SnapIDsMarkedStale is populated for
// force_cold_boot (warm + init tiers walked). On failure,
// Error carries the bounded dispatch error message (1 KB cap).
type OperatorIntentResponse struct {
	IntentID           string     `json:"intent_id"`
	Kind               string     `json:"kind"`
	Status             string     `json:"status"`
	TargetID           string     `json:"target_id"`
	AccountID          string     `json:"account_id,omitempty"`
	RequestedAt        time.Time  `json:"requested_at"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	Error              string     `json:"error,omitempty"`
	SnapIDsMarkedStale []string   `json:"snap_ids_marked_stale,omitempty"`
}

// SweepStuckBuildsResponse is the wire shape returned by POST
// /v1/admin/builds/sweep-stuck (P2c of the operator-side
// observability mega-PR, Commit 5c). SweptCount is 0 when no
// rows matched the threshold (the audit row is still emitted
// so the operator's "I checked" is durable).
type SweepStuckBuildsResponse struct {
	OK            bool   `json:"ok"`
	SweptCount    int    `json:"swept_count"`
	OlderThanSecs int    `json:"older_than_seconds"`
	ThresholdISO  string `json:"threshold_iso"`
}

// ThrottleSuggestionRow is one (route → suggested rate) row in the
// payload returned by GET /v1/apps/{slug}/throttle-suggestions
// (ADR-091 D20.5 amendment, issue #881 / PR-E). The recommender is
// read-only — it never auto-applies — and the suggestion is always
// ≤ the customer's plan ceiling (pkg/api.Limits.RateLimitRPS) so a
// customer can act on it without a 422 from apid's sub-plan
// validator.
//
// `Route` is the bounded label exactly as emitted on the Prometheus
// side: method + raw path (the same shape RouteRow.Route uses, ADR-093
// D6). The recommender never sees the reserved __route_other__
// overflow bucket — it is dropped from the suggestions slice and
// the count surfaces as RoutesCollapsed so the customer can tell
// "wildcard-shape pattern" from "low traffic on a real route".
//
// `ObservedRPS` is the rate() value over the window (already
// per-second). `SuggestedRPS` is `ceil(observed_rps * 2)` clamped
// into [1, plan.RateLimitRPS] — the 2× headroom is documented on
// the wire so the number is auditable rather than magic. The
// `Multiplier` field pins the formula version so a future change
// to the recommendation strategy can be distinguished from drift.
type ThrottleSuggestionRow struct {
	Route          string  `json:"route"`
	ObservedRPS    float64 `json:"observed_rps"`
	SuggestedRPS   float64 `json:"suggested_rps"`
	SuggestedBurst int     `json:"suggested_burst"`
	Multiplier     float64 `json:"multiplier"`
}

// ThrottleSuggestionsResponse is the wire shape for
// GET /v1/apps/{slug}/throttle-suggestions. Source degrades the
// same way AppMetricsResponse does: "prometheus" on success,
// "degraded: <reason>" otherwise. RouteMetricsDisabled is true
// when apps.route_metrics_enabled=false (Free plan) — the response
// carries empty Suggestions plus that flag so the dashboard can
// render the upsell rather than a misleading zero.
//
// RoutesCollapsed reports the count of routes that collapsed into
// __route_other__ during the window (ADR-093 cap = 50). It's a
// coverage signal, not a recommendation signal — a non-zero value
// tells the customer their throttle will be partial-coverage
// regardless of what limit they set.
type ThrottleSuggestionsResponse struct {
	AppID                string                  `json:"app_id"`
	Range                string                  `json:"range"`
	Source               string                  `json:"source"`
	AsOf                 string                  `json:"as_of"`
	RouteMetricsDisabled bool                    `json:"route_metrics_disabled"`
	RoutesCollapsed      int                     `json:"routes_collapsed"`
	PlanCeilingRPS       int                     `json:"plan_ceiling_rps"`
	PlanCeilingBurst     int                     `json:"plan_ceiling_burst"`
	Multiplier           float64                 `json:"multiplier"`
	Suggestions          []ThrottleSuggestionRow `json:"suggestions"`
	// Dry-run preview (ADR-104 amendment 5, issue #881 Phase 4
	// D1): when DryRun=true the request also supplied
	// CandidateRPS + CandidateBurst; WouldHaveRejected is one row
	// per route reporting the count of sub-windows where observed
	// rps exceeded the candidate (the customer can see "would
	// this have rejected N requests over the window?" before
	// committing). PerConsumerLimitNote is a static literal that
	// names the gateway_requests_by_route_total label gap so
	// customers reading the wire know the preview is rule-scope,
	// not consumer-scope.
	DryRun               bool                 `json:"dry_run,omitempty"`
	CandidateRPS         float64              `json:"candidate_rps,omitempty"`
	CandidateBurst       int                  `json:"candidate_burst,omitempty"`
	WouldHaveRejected    []ThrottlePreviewRow `json:"would_have_rejected,omitempty"`
	PerConsumerLimitNote string               `json:"per_consumer_limit_note,omitempty"`
}

// ThrottlePreviewRow is one row of the dry-run preview
// (ADR-104 amendment 5, issue #881 Phase 4 D1). For each
// non-collapsed route we report the count of sub-windows where the
// observed rate exceeded the candidate rps — a count of "would-have-
// rejected" requests over the window. WindowStart / WindowEnd echo
// the window edges so dashboards can render "N of M sub-windows" /
// "X of Y minutes" without re-deriving from Range.
//
// Per-consumer limitation: gateway_requests_by_route_total has no
// per-consumer labels today (Phase 3 per-consumer counts are
// scoped to the per-consumer limiter, not the Prometheus series).
// The preview therefore counts at the rule scope — the
// PerConsumerLimitNote field on ThrottleSuggestionsResponse
// surfaces this gap on the wire.
type ThrottlePreviewRow struct {
	Route        string  `json:"route"`
	CandidateRPS float64 `json:"candidate_rps"`
	OverCapCount float64 `json:"over_cap_count"`
	WindowStart  string  `json:"window_start"` // RFC3339Nano UTC
	WindowEnd    string  `json:"window_end"`   // RFC3339Nano UTC
}

// AppErrorsSummaryResponse is the wire body for
// GET /v1/apps/{slug}/errors/summary (ADR-096 PR-B). Items is the
// grouped top-N view: one row per (account_id, app_id, fingerprint)
// that fired within (since, until), sorted by count DESC, then
// last_seen_at DESC, then fingerprint ASC (mirrors the SQL ORDER BY
// in pkg/state/queries.sql::ListAppErrorGroups). WindowStart /
// WindowEnd echo the CLAMPED window after the AppErrorsWindowMaxHours
// cap; WindowClamped=true tells the dashboard that the requested
// span was wider than the server accepts.
//
// SampleMessage is the redacted string the writer stored (gatewayd-
// internal's Redactor.Apply trims to AppErrorsSampleMessageCapBytes
// BEFORE INSERT), so the dashboard renders it verbatim — but the
// handlers_app_errors_security_test.go grep tripwire re-checks every
// SampleMessage against the email + card regex on the way out.
type AppErrorsSummaryResponse struct {
	GeneratedAt   string                `json:"generated_at"` // RFC3339Nano UTC, mirrors AppSLOResponse.AsOf
	AppID         string                `json:"app_id"`
	AppSlug       string                `json:"app_slug"`
	WindowStart   string                `json:"window_start"`          // RFC3339Nano UTC
	WindowEnd     string                `json:"window_end"`            // RFC3339Nano UTC
	WindowClamped bool                  `json:"window_clamped"`        // true iff requested span > AppErrorsWindowMaxHours
	Items         []AppErrorSummaryItem `json:"items"`                 // never nil; JSON emits [] on empty (same contract as AppRoutesResponse.Routes)
	NextCursor    string                `json:"next_cursor,omitempty"` // opaque (last_seen_at, fingerprint) base64; mirrors pkg/cursor/cursor.go:52
	Limit         int                   `json:"limit"`                 // echo of the post-clamp limit applied
}

// AppErrorSummaryItem is one row of the summary top-N. The
// (Fingerprint, ErrorClass, Route, HTTPStatus) tuple is the grouping
// key; Count is the issue count (post-dedupe), RequestCount is the
// total distinct app_error_requests rows linked to this fingerprint.
// The two diverge after a dedupe-merge within the dedupe window
// (AppErrorsDedupeWindowSeconds).
type AppErrorSummaryItem struct {
	Fingerprint   string `json:"fingerprint"` // 64-char lowercase hex
	ErrorClass    string `json:"error_class"` // closed-vocab enum (CHECK constraint)
	Route         string `json:"route"`       // matched template, NOT expanded URL
	HTTPStatus    int32  `json:"http_status"`
	Count         int64  `json:"count"`          // issue count (post-dedupe)
	RequestCount  int64  `json:"request_count"`  // distinct app_error_requests rows
	FirstSeenAt   string `json:"first_seen_at"`  // RFC3339Nano UTC
	LastSeenAt    string `json:"last_seen_at"`   // RFC3339Nano UTC
	SampleMessage string `json:"sample_message"` // already redacted at write time
}

// AppErrorRequestsResponse is the body of
// GET /v1/apps/{slug}/errors/{fingerprint} (ADR-096 PR-B). The four
// header fields (Fingerprint, ErrorClass, Route, HTTPStatus) are
// denormalised here so the drill-down page header renders without a
// second round-trip. Requests is the cursor-paginated window over
// app_error_requests for this fingerprint, ordered by received_at
// DESC, request_id DESC (matches the
// app_error_requests_drill_idx path).
type AppErrorRequestsResponse struct {
	Fingerprint string                `json:"fingerprint"`
	ErrorClass  string                `json:"error_class"`
	Route       string                `json:"route"`
	HTTPStatus  int32                 `json:"http_status"`
	Requests    []AppErrorRequestItem `json:"requests"` // never nil
	NextCursor  string                `json:"next_cursor,omitempty"`
}

// AppErrorRequestItem is one row of the drill-down. RequestID is the
// gateway's x-faas-request-id (uuid v7). SampleMessage is the
// redacted string the writer stored. DeploymentID is the deployment
// the error originated from; nil when the deployment has been
// deleted (FK ON DELETE SET NULL).
type AppErrorRequestItem struct {
	RequestID     string `json:"request_id"`  // uuid v7 from gateway
	ReceivedAt    string `json:"received_at"` // RFC3339Nano UTC
	Route         string `json:"route"`
	HTTPStatus    int32  `json:"http_status"`
	ErrorClass    string `json:"error_class"`
	SampleMessage string `json:"sample_message"`
	DeploymentID  string `json:"deployment_id,omitempty"` // uuid string, omitted when NULL
}

// AppErrorSampleResponse is the body of
// GET /v1/apps/{slug}/errors/{fingerprint}/first (ADR-096 PR-B).
// Embeds AppErrorRequestItem so the wire shape is uniform across the
// drill-down list and the /first endpoint; HeadersSample is the
// jsonb-decoded header subset the writer stored (max 8 keys,
// 256 bytes/value, total ≤8 KiB per the schema CHECK); RedactionsApplied
// is the sorted-unique list of pattern names the Redactor.Apply +
// Redactor.ApplyHeaders calls applied (matches pkg/redact/redact.go:85).
//
// nil-when-empty contract: a request that didn't match any pattern
// has RedactionsApplied=nil (the writer's Redactor returns (s, nil)
// for no-match). The dashboard renders "No redactions" on nil.
type AppErrorSampleResponse struct {
	AppErrorRequestItem
	HeadersSample     map[string]string `json:"headers_sample"`
	RedactionsApplied []string          `json:"redactions_applied"`
}

// RetryDeploymentRequest (ADR-117 §Production-ready follow-on, C2)
// is the body of POST /v1/apps/{slug}/deployments/{id}/retry.
// FromStage MUST be one of the closed-6 stage vocabulary
// (pkg/state.AllStageNames); the apid handler validates via
// state.IsStageName before the storage call. The CLI's
// `gregale deploys retry <id> --from=<stage>` builds this
// payload verbatim.
type RetryDeploymentRequest struct {
	FromStage string `json:"from_stage"`
}

// OpenAPIDocResponse is the typed wire envelope for the OpenAPI doc
// stored per-deployment (issue #975 item #1 / ADR-122). The probe
// runs unconditionally during cold boot; the apid surfaces the doc
// only on paid plans (Hobby/Pro/Scale). Free plans return 402
// + openapi_docs_not_allowed from the handler.
//
// Doc is the raw OpenAPI document body, returned verbatim from the
// customer's /openapi.json — the server does no rewriting. Source is
// the closed enum (cold_boot | manual_upload); ByteSize is what the
// handler enforces against Plan.OpenAPIDocMaxBytes(); Truncated is
// true when the cold-boot probe clipped the body at 128 KiB.
type OpenAPIDocResponse struct {
	DeploymentID string         `json:"deployment_id"`
	AccountID    string         `json:"account_id"`
	AppID        string         `json:"app_id"`
	Source       string         `json:"source"`
	ByteSize     int            `json:"byte_size"`
	DocSHA256    string         `json:"doc_sha256,omitempty"`
	Truncated    bool           `json:"truncated"`
	CapturedAt   string         `json:"captured_at"`
	UpdatedAt    string         `json:"updated_at"`
	Doc          map[string]any `json:"doc"`
}

// AppOpenAPIImportResponse is the typed wire envelope for the
// POST /v1/apps/{slug}/openapi import endpoint (issue #975 item #2 /
// ADR-126). Mirrors the row shape of app_openapi_docs (one row per
// app, last-write-wins). Source is always "manual_import" — cold-boot
// captures go to deployment_openapi_docs (item #1), not here.
//
// EndpointCount is the number of HTTP operations in the imported
// doc's paths.*; ByteSize is the raw body size the handler enforced
// against state.OpenAPIImportMaxDocBytes (256 KiB).
type AppOpenAPIImportResponse struct {
	AppID          string `json:"app_id"`
	Source         string `json:"source"`
	OpenAPIVersion string `json:"openapi_version"`
	EndpointCount  int    `json:"endpoint_count"`
	ByteSize       int    `json:"byte_size"`
	CapturedAt     string `json:"captured_at"`
	UpdatedAt      string `json:"updated_at"`
}

// AppOpenAPIImportDryRunResponse is the typed wire envelope for the
// POST /v1/apps/{slug}/openapi/dry-run endpoint (ADR-126 D3). The
// customer pastes each Suggestion's Path + Methods + Kind + Action
// into the existing create-edge-rule endpoint. Empty Suggestions
// when the doc is fully covered by existing validate edge rules.
type AppOpenAPIImportDryRunResponse struct {
	Suggestions    []EdgeRuleSuggestion `json:"suggestions"`
	OpenAPIVersion string               `json:"openapi_version"`
	EndpointCount  int                  `json:"endpoint_count"`
}

// EdgeRuleSuggestion is a single read-only candidate row in the
// dry-run response. Mirrors the create-edge-rule request body
// fields so the customer can copy paste. The Kind + Action union
// shape matches pkg/api/dto.go's existing EdgeRule*Action types.
type EdgeRuleSuggestion struct {
	Path    string         `json:"path"`
	Methods []string       `json:"methods"`
	Kind    string         `json:"kind"`
	Action  map[string]any `json:"action"`
}

// DebugTelemetryRequestItem is one row of per-app request telemetry
// returned by GET /v1/apps/{slug}/debug/requests (ADR-127 / PR-A).
// The fields are 1:1 with the request_telemetry table columns; the
// apid handler maps sqlc-generated rows to this wire DTO (cmd/apid
// uses the sqlc row directly because pkg/api cannot import
// pkg/state/sqlc without an import cycle).
type DebugTelemetryRequestItem struct {
	ID           string  `json:"id"`
	DeploymentID string  `json:"deployment_id"`
	Route        string  `json:"route"`
	Method       string  `json:"method"`
	Status       int     `json:"status"`
	LatencyMS    int     `json:"latency_ms"`
	ColdBoot     bool    `json:"cold_boot"`
	TraceID      *string `json:"trace_id"`
	ReceivedAt   string  `json:"received_at"`
}

// DebugTelemetryListResponse is the wire envelope for the debug
// requests list endpoint. `Since` echoes the effective window
// applied (after the plan's DebugTelemetryRetentionDays clamp) so
// the dashboard can surface a "you widened past the cap" tile when
// a customer asks for a longer window than their plan permits.
type DebugTelemetryListResponse struct {
	Since    string                      `json:"since"`
	Requests []DebugTelemetryRequestItem `json:"requests"`
}

// RequestAnalyticsRoute is one aggregated route/method row returned by
// GET /v1/apps/{slug}/analytics. Counts include the request_telemetry row's
// collapsed count, while latency percentiles are weighted by that count.
type RequestAnalyticsRoute struct {
	Route         string  `json:"route"`
	Method        string  `json:"method"`
	Requests      int64   `json:"requests"`
	ErrorRequests int64   `json:"error_requests"`
	ErrorRatePct  float64 `json:"error_rate_pct"`
	ColdBoots     int64   `json:"cold_boots"`
	P50MS         int     `json:"p50_ms"`
	P95MS         int     `json:"p95_ms"`
	P99MS         int     `json:"p99_ms"`
}

// RequestAnalyticsResponse is the bounded historical request analytics
// envelope for one app. Since/Until are the effective half-open window; a
// longer requested since value is represented by WindowClamped=true.
type RequestAnalyticsResponse struct {
	Slug            string                  `json:"slug"`
	Since           string                  `json:"since"`
	From            string                  `json:"from"`
	Until           string                  `json:"until"`
	WindowClamped   bool                    `json:"window_clamped"`
	Requests        int64                   `json:"requests"`
	ErrorRequests   int64                   `json:"error_requests"`
	ErrorRatePct    float64                 `json:"error_rate_pct"`
	ColdBoots       int64                   `json:"cold_boots"`
	P50MS           int                     `json:"p50_ms"`
	P95MS           int                     `json:"p95_ms"`
	P99MS           int                     `json:"p99_ms"`
	Routes          []RequestAnalyticsRoute `json:"routes"`
	RoutesLimit     int                     `json:"routes_limit"`
	RoutesTruncated bool                    `json:"routes_truncated"`
	AsOf            string                  `json:"as_of"`
}

// RequestAnalyticsTimeseriesPoint is one UTC-aligned hourly bucket returned
// by GET /v1/apps/{slug}/analytics/timeseries. Counts include collapsed
// request-telemetry row weights, and latency percentiles use the same weights.
type RequestAnalyticsTimeseriesPoint struct {
	Start         string  `json:"start"`
	Requests      int64   `json:"requests"`
	ErrorRequests int64   `json:"error_requests"`
	ErrorRatePct  float64 `json:"error_rate_pct"`
	ColdBoots     int64   `json:"cold_boots"`
	P50MS         int     `json:"p50_ms"`
	P95MS         int     `json:"p95_ms"`
	P99MS         int     `json:"p99_ms"`
}

// RequestAnalyticsTimeseriesResponse is the zero-filled hourly series used
// for customer-facing request analytics charts. The effective window is
// bounded by the account's telemetry retention.
type RequestAnalyticsTimeseriesResponse struct {
	Slug          string                            `json:"slug"`
	Route         string                            `json:"route,omitempty"`
	Method        string                            `json:"method,omitempty"`
	Since         string                            `json:"since"`
	From          string                            `json:"from"`
	Until         string                            `json:"until"`
	WindowClamped bool                              `json:"window_clamped"`
	Bucket        string                            `json:"bucket"`
	Points        []RequestAnalyticsTimeseriesPoint `json:"points"`
	AsOf          string                            `json:"as_of"`
}

// DebugRegressionItem is one row of debug_regression_observations
// (ADR-127 / PR-B) returned by GET /v1/apps/{slug}/debug/regressions.
// Factor is a NUMERIC(5,2) so the wire serialises as a JSON
// number with up to 2 decimal places (1.20, 1.05, 2.43).
type DebugRegressionItem struct {
	DeploymentID    string `json:"deployment_id"`
	Route           string `json:"route"`
	P95MS           int    `json:"p95_ms"`
	P95BaseMS       int    `json:"p95_base_ms"`
	AffectedCount   int    `json:"affected_count"`
	Factor          string `json:"regression_factor"`
	FirstDetectedAt string `json:"first_detected_at"`
	LastDetectedAt  string `json:"last_detected_at"`
}

// DebugRegressionsResponse is the wire envelope for the debug
// regressions endpoint. `Since` echoes the effective window
// applied (after the plan's DebugTelemetryRetentionDays clamp).
type DebugRegressionsResponse struct {
	Since       string                `json:"since"`
	Regressions []DebugRegressionItem `json:"regressions"`
}

// DebugCompareRequest is the body shape for POST /v1/apps/{slug}
// /debug/compare (ADR-127 / PR-B). Source and mirror are
// deployment_ids; route is optional (empty = all routes); since
// and until constrain the window.
type DebugCompareRequest struct {
	Source string `json:"source"`
	Mirror string `json:"mirror"`
	Route  string `json:"route,omitempty"`
	Since  string `json:"since,omitempty"` // duration string ("3h", "7d")
	Until  string `json:"until,omitempty"` // RFC3339 or empty for now
}

// DebugCompareRouteStats is the per-route stats row in the
// compare response. P50/P95/P99 are computed from the same
// percentile_cont aggregate as RequestTelemetryBaselineP95ByRoute
// (PR-A). Count is the row count in the window for that route.
type DebugCompareRouteStats struct {
	Route     string `json:"route"`
	SourceP50 int    `json:"source_p50_ms"`
	SourceP95 int    `json:"source_p95_ms"`
	SourceP99 int    `json:"source_p99_ms"`
	SourceN   int64  `json:"source_count"`
	MirrorP50 int    `json:"mirror_p50_ms"`
	MirrorP95 int    `json:"mirror_p95_ms"`
	MirrorP99 int    `json:"mirror_p99_ms"`
	MirrorN   int64  `json:"mirror_count"`
}

// DebugCompareResponse is the wire envelope for the debug compare
// endpoint. Routes is empty when both deployments had no traffic
// in the window.
type DebugCompareResponse struct {
	Source string                   `json:"source"`
	Mirror string                   `json:"mirror"`
	Routes []DebugCompareRouteStats `json:"routes"`
}

// DebugReplayResponse is the wire envelope for the debug replay
// endpoint. PR-B returns the underlying mirror invocation
// identifier; PR-C's LLM-synthesis layer will populate the prose
// body. PR-B deliberately keeps the response shape minimal — the
// real value of replay surfaces in PR-C.
type DebugReplayResponse struct {
	MirrorInvocationID string `json:"mirror_invocation_id,omitempty"`
	Status             string `json:"status"`
}

// ---- SAFE-RELEASES-R (issue #976 / ADR-122 / Mega PR #2 commit 6) ----

// AllowedRecoverRolloutActions is the closed-set vocabulary for
// the RecoverRolloutRequest.Action field. Mirrors the
// cmd/gregale CLI parser's local closed-set check and the
// handler's nil-coerced default (handler maps missing Action to
// ErrInvalidRecoverAction at the validation boundary).
var AllowedRecoverRolloutActions = []string{"advance", "promote", "abort"}

// AllowedRecoverRolloutAction returns true iff v is a member of
// AllowedRecoverRolloutActions. Mirrors the membership-helper
// pattern at AllowedAlertRuleAction / AllowedAlertRuleMetric.
func AllowedRecoverRolloutAction(v string) bool {
	for _, a := range AllowedRecoverRolloutActions {
		if a == v {
			return true
		}
	}
	return false
}

// RecoverRolloutRequest is the body for
// POST /v1/apps/{slug}/rollouts/recover — the operator manual-
// recovery escape hatch when the safedeploy orchestrator's
// 30-min stuck-rollout detector fires and the operator wants to
// push the canary forward (or short-circuit to 100% via promote,
// or hard-abort). The CLI subcommand `gregale rollouts recover
// <slug> --action advance|promote|abort --reason <text>` sends
// this body. Plan-gated via api.Plan.TrafficSplitAllowed() —
// Free plan returns 403.
//
// Action semantics (mirrors the handler-level state machine):
//
//   - "advance" — bump canary_step by 1 (capped at
//     canary_total_steps - 1), stamp canary_step_started_at = now,
//     and redistribute traffic via the existing
//     state.UpdateDeploymentTraffic. Requires the rollout to be
//     in {pending, rolling_out} AND the row must be stuck (>30min
//     since canary_step_started_at). The "stuck" gate is the
//     reason this exists — healthy rollouts advance on their own
//     via pkg/canary; manual recovery is the operator override.
//
//   - "promote" — short-circuit the canary ladder to 100%
//     traffic on the in-flight deployment + zero siblings so Σ
//     stays 100 by construction. rollout_state flips to
//     'complete'. Requires rollout_state ∈ {pending,
//     rolling_out}. No stuck-check (the operator is explicitly
//     declaring "ship it").
//
//   - "abort" — flip rollout_state to 'aborted', stamp
//     rollout_aborted_at = now + the operator's reason text into
//     rollout_aborted_reason. The deployment row stays 'live'
//     with whatever traffic_percent it currently has (the
//     operator is responsible for `gregale deploys rollback`
//     if they want to fully revert). Requires rollout_state ∈
//     {pending, rolling_out}.
type RecoverRolloutRequest struct {
	// Action is the closed-set verb ∈ {advance, promote, abort}.
	Action string `json:"action"`
	// Reason is a free-form operator note captured into the
	// deployment_audit row's Data payload. The plan gate does
	// NOT require a reason — but the cmd/gregale CLI marks
	// --reason as required (operators writing a recovery note
	// is the entire point of the audit trail).
	Reason string `json:"reason,omitempty"`
}

// RolloutTransitionResponse is the body returned by
// POST /v1/apps/{slug}/rollouts/recover. The Deployment carries
// the post-transition state (rollout_state + canary_step +
// rollout_completed_at / rollout_aborted_at). AuditID is the
// deployment_audit row id so the operator CLI can echo it as
// a chip on the terminal — the operator's "what happened"
// timeline starts at this row.
type RolloutTransitionResponse struct {
	Deployment DeploymentResponse `json:"deployment"`
	AuditID    string             `json:"audit_id"`
}

// --- Jobs (issue #1184 Workstream A / ADR-099) ----------------------
//
// JobTemplate is the customer-facing stored definition (the
// equivalent of an app's static config). Each JobTemplate owns
// zero or more JobRuns; each run fans out into JobTasks that
// the dispatch tick turns into microVMs. The shape mirrors
// state.Job / state.JobRun / state.JobTask but is the wire
// projection — only the fields a customer or the dashboard
// should see.
//
// Plan gating: CreateJobRequest validation enforces Free=0
// (ErrPlanJobsNotAllowed → 402 CodeJobsNotAllowed) at the
// handler, not the DTO. The DTO carries the input verbatim;
// the plan check lives in the handler so a Free customer's
// POST gets the upgrade-to-Hobby copy the dashboard renders
// rather than a 400 validation error.
//
// Field semantics match state.Job's struct docstrings; see
// pkg/state/jobs.go. The api.Plan getter is used by the
// handler to clamp RAM / task-timeout / parallelism / retry
// max / tasks per run against the per-plan caps.

// CreateJobRequest is the POST /v1/jobs body.
type CreateJobRequest struct {
	// Name is the customer slug (jobs.name UNIQUE per
	// account_id). 3-40 chars, lowercase letters / digits /
	// hyphens. Validated by the handler against validSlug.
	Name string `json:"name"`
	// Kind is the closed-set {batch, recurring}. batch
	// tasks run exactly once; recurring tasks re-run on
	// the run's schedule (issue #1184 Workstream A
	// extension — base schema accepts both). Defaults to
	// "batch" when empty.
	Kind string `json:"kind,omitempty"`
	// ImageRef is the OCI image name[:tag | @digest].
	// Digest pinning is RECOMMENDED — the same way app
	// builds are — but not enforced at this layer.
	ImageRef string `json:"image_ref"`
	// Command is the OCI entrypoint. Capped at 64 entries
	// by migrations/00572 jobs_command_min_chk.
	Command []string `json:"command"`
	// EnvOverrides is the open-vocabulary jsonb map of
	// environment variables to inject into every task of
	// every run. Per-run overrides (env_overrides on
	// CreateJobRunRequest) win at task-execution time.
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
	// RAMMB is the billable memory (migrations/00571 +
	// 00572 jobs_command.sql + per-plan caps). 0 →
	// handler applies Plan.JobRAMMB default.
	RAMMB int `json:"ram_mb,omitempty"`
	// TaskTimeoutSec is the per-task wall-clock deadline.
	// Past this, guest-init SIGTERMs the process then
	// SIGKILLs after a 30s grace, and exits with
	// error_class='timeout' (exit_code=124). 0 → handler
	// applies Plan.JobTaskTimeoutSec default.
	TaskTimeoutSec int `json:"task_timeout_sec,omitempty"`
	// MaxParallelism caps concurrent tasks across the
	// run (the dispatch tick holds the cap).
	// 0 → handler applies Plan.JobMaxParallelismPerRun
	// default. Hard ceiling = JobMaxParallelismPerRun.
	MaxParallelism int `json:"max_parallelism,omitempty"`
	// RetryMax is the per-task max retries after a
	// non-success exit. attempt=1 means "no retries"; the
	// dispatch tick increments up to (RetryMax+1).
	// 0 → handler applies the plan cap.
	RetryMax int `json:"retry_max,omitempty"`
}

// UpdateJobRequest is the PATCH /v1/jobs/{name} body. nil
// pointers leave the column untouched (mirrors the app PATCH
// convention; see api.UpdateAppRequest).
type UpdateJobRequest struct {
	ImageRef       *string           `json:"image_ref,omitempty"`
	Command        []string          `json:"command,omitempty"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
	RAMMB          *int              `json:"ram_mb,omitempty"`
	TaskTimeoutSec *int              `json:"task_timeout_sec,omitempty"`
	MaxParallelism *int              `json:"max_parallelism,omitempty"`
	RetryMax       *int              `json:"retry_max,omitempty"`
	// Status is the open-set {active, paused}. Setting
	// status='paused' halts future dispatches without
	// killing live tasks (the dispatch tick skips paused
	// jobs at JobTaskClaimBatch). Soft-delete uses
	// DELETE /v1/jobs/{name} (separate status='deleted'
	// transition with the no-live-instances guard).
	Status *string `json:"status,omitempty"`
}

// CreateJobRunRequest is the POST /v1/jobs/{name}/runs body.
// Atomic fan-out via a single generate_series INSERT (see
// state.PgStore.JobRunCreate); the handler validates the
// Tasks count against Plan.JobMaxTasksPerRun (Hobby=100,
// Pro=1000, Scale=5000) before the store call.
type CreateJobRunRequest struct {
	// Tasks is the number of tasks to fan out. Each
	// task has its own (run_id, task_index) — task_index
	// runs 1..Tasks. Tasks=1 is the "single-shot job"
	// shape (the dashboard's one-off cron replacement).
	Tasks int `json:"tasks"`
	// Parallelism overrides the job's MaxParallelism
	// for THIS run only. nil → job.MaxParallelism.
	Parallelism *int `json:"parallelism,omitempty"`
	// RetryMax overrides the job's RetryMax for THIS
	// run only. nil → job.RetryMax.
	RetryMax *int `json:"retry_max,omitempty"`
	// TaskTimeoutSec overrides the job's TaskTimeoutS
	// for THIS run only. nil → job.TaskTimeoutS.
	TaskTimeoutSec *int `json:"task_timeout_sec,omitempty"`
	// EnvOverrides is merged with the job's
	// env_overrides at task-execution time;
	// run-level wins. nil → no per-run overrides.
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
}

// JobResponse is the wire projection of state.Job. Stable
// across all /v1/jobs endpoints so the SDK can decode with a
// single type. CreatedAt / UpdatedAt are RFC 3339 strings
// (matches the AppResponse convention).
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
// Includes the aggregated counters the dashboard renders as
// "X/Y succeeded" (succeeded / failed / cancelled / running).
// dead_letter_count (added in migrations/00574) is the
// retry-exhaustion counter — a run is "dead letter" when
// dead_letter_count > 0 AND aggregate_status='dead_letter'.
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
// ErrorClass is the canonical mapped string from
// mapExitToTerminalStatus; ExitCode is the raw guest exit.
// LeaseToken is omitted (internal dispatch primitive, not a
// customer-facing field).
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

// JobTaskLogResponse is the body of GET /v1/jobs/{name}/runs/
// {id}/tasks/{idx}/logs. Logs are read from vmmd's tail
// endpoint (same path the dashboard uses for live app logs);
// the handler proxies the call to the compute node that owns
// the instance and streams back the last N bytes.
//
// Truncated=true means the tail was capped at MaxBytes;
// clients should re-fetch with a larger limit to see more.
// Empty LogContent with Truncated=false means the task never
// produced output (the process exited before writing
// anything — common for OOM-killed tasks).
type JobTaskLogResponse struct {
	TaskStatus string `json:"task_status"`
	LogContent string `json:"log_content"`
	Truncated  bool   `json:"truncated"`
	MaxBytes   int    `json:"max_bytes"`
}

// ListJobsResponse is the body of GET /v1/jobs. Page-based
// pagination — limit / offset are query parameters (handler
// clamps limit to [1, 200]). NextOffset is -1 when there are
// no more pages (consistent with the app list endpoint).
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

// ListJobTasksResponse is the body of GET /v1/jobs/{name}/runs/
// {id}/tasks.
type ListJobTasksResponse struct {
	Tasks      []JobTaskResponse `json:"tasks"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
	NextOffset int               `json:"next_offset"`
	Total      int               `json:"total"`
}

// JobRunCancelledResponse is the body of POST /v1/jobs/{name}/
// runs/{id}/cancel. Returns the post-cancel run aggregate so
// the dashboard can re-render without a separate GET.
type JobRunCancelledResponse struct {
	Run         JobRunResponse `json:"run"`
	CancelledAt string         `json:"cancelled_at"`
}

// JobDeletedResponse is the body of DELETE /v1/jobs/{name}.
// Distinct from JobResponse (no command / env / caps —
// the dashboard only needs to render the deletion chip +
// the "was soft-deleted at" timestamp).
type JobDeletedResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
}

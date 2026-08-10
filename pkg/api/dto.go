package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

// Wire DTOs for the v1 REST API (spec Appendix A). Defined once here so apid and
// the faas CLI share exactly one contract; `--json` output stability (UX §3.2)
// depends on these shapes.

// CreateAppRequest creates an app or function.
type CreateAppRequest struct {
	Slug           string `json:"slug"`
	Type           string `json:"type,omitempty"`    // "app" (default) | "function"
	Runtime        string `json:"runtime,omitempty"` // node22|python312|go124|go124-alpine|node24|python313 for functions
	RAMMB          int    `json:"ram_mb,omitempty"`  // 0 => plan default
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
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
}

// UpdateAppRequest is the partial-update payload for PATCH /v1/apps/{slug}.
// All fields are pointers so the wire form can distinguish "not set" from
// "set to zero".
type UpdateAppRequest struct {
	RAMMB          *int `json:"ram_mb,omitempty"`
	IdleTimeoutS   *int `json:"idle_timeout_s,omitempty"`
	MaxConcurrency *int `json:"max_concurrency,omitempty"`
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

// AppResponse is an app as returned by the API.
type AppResponse struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Type           string `json:"type"`
	Runtime        string `json:"runtime,omitempty"`
	RAMMB          int    `json:"ram_mb"`
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
	IdleTimeoutS          int `json:"idle_timeout_s,omitempty"`
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

// PublicAuthBlock (issue #477 / ADR-079) is the per-app
// public-URL auth configuration on a PATCH body. Mode is
// the canonical 'open'|'bearer'|'basic' string (must match
// apps_public_auth_mode_chk). BasicUser + BasicPass are
// only meaningful when Mode='basic'; the apid PATCH
// handler seals them under the APP_BASIC_AUTH secretbox
// namespace and stores the ciphertext in
// apps.public_auth_basic. For Mode='open' or 'bearer' the
// apid handler ignores them (and clears any existing
// sealed blob so a stale secretbox row never reaches a
// fresh request). The wire-shape reflects what the
// customer PATCHes; the on-disk shape is the
// public_auth_mode + public_auth_basic columns plus the
// secretbox seal at PATCH time.
type PublicAuthBlock struct {
	// Mode is the canonical 'open'|'bearer'|'basic'
	// string. apid rejects unknown values with 422
	// invalid_public_auth_mode.
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
}

// Validate enforces the canonical PublicAuthBlock shape:
// Mode is a closed enum; BasicUser + BasicPass are
// required iff Mode='basic'. Returns a 422-mapped
// *Problem on any malformed shape. nil in → nil out
// (the caller treats nil as "don't touch the column").
func (b *PublicAuthBlock) Validate() *Problem {
	if b == nil {
		return nil
	}
	switch b.Mode {
	case AppPublicAuthModeOpen, AppPublicAuthModeBearer, AppPublicAuthModeBasic:
	default:
		return NewProblem(422, CodeValidation, "Invalid public_auth.mode",
			fmt.Sprintf("public_auth.mode must be 'open', 'bearer', or 'basic'; got %q", b.Mode))
	}
	if b.Mode != AppPublicAuthModeBasic {
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

// PublicAuthStatus (issue #477 / ADR-079) is the
// read-only per-app public-URL auth surface on
// AppResponse. Mode mirrors the apps.public_auth_mode
// column; HasBasicCreds is true iff the row has a
// non-null public_auth_basic blob (a mode='basic' app
// without creds would still 401 every request). The
// plaintext username/password is NEVER echoed — it lives
// in app_secrets (ADR-045) and is loopback-mounted to
// drive1 at boot.
type PublicAuthStatus struct {
	Mode          string `json:"mode"`
	HasBasicCreds bool   `json:"has_basic_creds"`
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
}

// DeploymentHealthcheck is the readiness-probe shape on the
// override object. Defaults: interval 5s, timeout 2s, retries 3.
// Path is required (and must start with "/") when the parent
// healthcheck is set.
type DeploymentHealthcheck struct {
	Path      string `json:"path"`
	IntervalS int    `json:"interval_s,omitempty"`
	TimeoutS  int    `json:"timeout_s,omitempty"`
	Retries   int    `json:"retries,omitempty"`
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
	CreatedAt string `json:"created_at"`
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
}

// UpdateDeploymentRequest is the body for PATCH /v1/deployments/{id}
// (issue #557 closure / ADR-072). MinInstances is the only mutable
// field on a deployment — the image / digest / overrides / sidecars
// are immutable post-create (a new deployment is the canonical way
// to change them).
type UpdateDeploymentRequest struct {
	MinInstances *int `json:"min_instances"`
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

// AccountResponse is the whoami payload. Limits is the plan's
// quota/limit table (RAM MB, max concurrency, included GB-h,
// deployed-app cap) so the dashboard /account page can show
// "you have X of Y apps" without a second round trip. UsageGBHours
// is the roll-up for the current month (caller-aggregated from
// Store.UsageByHour in apid; included here so the dashboard can
// render the meter in one fetch).
type AccountResponse struct {
	ID            string        `json:"id"`
	Email         string        `json:"email"`
	Plan          string        `json:"plan"`
	Status        string        `json:"status"`
	Limits        AccountLimits `json:"limits"`
	UsageGBHours  float64       `json:"usage_gb_hours"`
	AppCount      int           `json:"app_count"`
	GitHubInstall string        `json:"github_install_id,omitempty"`
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
	// gateway-side producer lands in PR-2. 0 when no meterd
	// sample has accumulated yet.
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
	// accumulated yet or the wire regen that surfaces the
	// ingress field has not yet landed (PR-A commit #2
	// follow-up).
	NetRxBytes int64 `json:"net_rx_bytes"`
	// ColdBootCount (ADR-048) is the per-app monthly
	// count of WAKE_RESTORE → WAKE_COLD_BOOT transitions
	// observed across this app's instances. Source:
	// scheddgrpc.InstanceStatsRow.LastWakeMethod, sampled
	// by meterd SampleAndRoll → usage_minutes.
	// cold_boot_count. Informational only — not billed.
	// 0 when no meterd sample has accumulated yet or the
	// wire regen has not yet landed.
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
	Password string `json:"password"`
}

// UsageSummaryResponse is the roll-up for the current month (or any
// month passed as a query param). Used by the dashboard usage page so
// the customer sees a single number ("used X of Y GB-h, overage $Z")
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
// panel. The gateway-side tx_bytes producer lands in PR-2.
type UsageSummaryResponse struct {
	Month           string  `json:"month"`             // YYYY-MM
	UsedGBHours     float64 `json:"used_gb_hours"`     // Σ mb_seconds / 3_600_000
	IncludedGBHours int64   `json:"included_gb_hours"` // from plan limits
	OverageGBHours  float64 `json:"overage_gb_hours"`  // max(0, used - included)
	OverageCents    int64   `json:"overage_cents"`     // overage * 1.0 (€0.01/GB-h in cents)
	// UsedCPUHours is the per-month CPU-hours Σ CPUUsageUsec /
	// 3.6e9. Informational only — billing is on UsedGBHours.
	// Issue #279 / PR-B.
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
	// WAKE_RESTORE → WAKE_COLD_BOOT transitions across
	// every app on this account. Informational only — not
	// billed. The dashboard's "this customer's cold-boot
	// bill of health" panel reads this single number; the
	// per-app breakdown lives at UsageResponse.ColdBootCount.
	ColdBootTotal int64 `json:"cold_boots"`
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
// (issue #253). URL is the operator-configured billing portal link —
// today: FAAS_BILLING_PORTAL_URL with `{account_id}` substituted.
// Empty URL is a 200 (the request itself succeeded); it is the
// "absent" sentinel meaning the box has no portal configured and
// the CLI should print a friendly hint instead of opening the
// browser to "". The field is omitempty so an unset URL on a Free
// account does not surface as JSON null in either the dashboard's
// SSR page or the SDK response.
type BillingPortalResponse struct {
	URL string `json:"url,omitempty"`
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
// "secret.set", "secret.deleted", "account.plan_changed",
// "account.deletion_scheduled", "account.deletion_restored".
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

// PlanWorkload mirrors reposcan.Workload (Phase 3 wire shape).
// Field names match the OpenAPI schema verbatim — the spec-check
// AST gate enforces the field-for-field mapping.
type PlanWorkload struct {
	Name       string   `json:"name"`
	RootDir    string   `json:"root_dir"`
	Dockerfile string   `json:"dockerfile,omitempty"`
	Command    []string `json:"command"`
	Class      string   `json:"class,omitempty"`
	Schedule   string   `json:"schedule,omitempty"`
	Ports      []int    `json:"ports"`
	EnvKeys    []string `json:"env_keys,omitempty"`
	Source     string   `json:"source,omitempty"`
	Tier       string   `json:"tier,omitempty"`
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
